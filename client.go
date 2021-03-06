package meniscus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"sync"
	"time"
)

//HTTPClient ...
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

//BulkClient ...
type BulkClient struct {
	httpclient HTTPClient
	timeout    time.Duration
}

type requestParcel struct {
	request *http.Request
	index   int
}

type roundTripParcel struct {
	response *http.Response
	request  *http.Request // this is required to recreate a http.Response with a new http.Request without a context
	err      error
	index    int
}

//NewBulkHTTPClient ...
func NewBulkHTTPClient(client HTTPClient, timeout time.Duration) *BulkClient {
	return &BulkClient{
		httpclient: client,
		timeout:    timeout,
	}
}

type roundTripChannels struct {
	requestList        chan requestParcel
	receivedResponses  chan roundTripParcel
	processedResponses chan roundTripParcel
	collectResponses   chan []roundTripParcel
}

func newRoundTripChannels() roundTripChannels {
	return roundTripChannels{
		requestList:        make(chan requestParcel),
		receivedResponses:  make(chan roundTripParcel),
		processedResponses: make(chan roundTripParcel),
		collectResponses:   make(chan []roundTripParcel),
	}
}

//Do ...
func (cl *BulkClient) Do(bulkRequest *RoundTrip) ([]*http.Response, []error) {
	noOfRequests := len(bulkRequest.requests)
	if noOfRequests == 0 {
		return nil, []error{ErrNoRequests}
	}

	bulkRequest.responses = make([]*http.Response, noOfRequests)
	bulkRequest.errors = make([]error, noOfRequests)

	roundTripChannels := newRoundTripChannels()

	stopProcessing := make(chan struct{})
	defer close(stopProcessing)

	ctx, cancel := context.WithTimeout(context.Background(), cl.timeout)
	defer cancel()

	for index, req := range bulkRequest.requests {
		bulkRequest.requests[index] = req.WithContext(ctx)
	}

	go cl.responseMux(ctx,
		bulkRequest,
		roundTripChannels.processedResponses,
		roundTripChannels.collectResponses)
	go cl.workerManager(ctx,
		bulkRequest,
		&roundTripChannels,
		stopProcessing)

	cl.completionListener(bulkRequest, roundTripChannels.collectResponses)

	return bulkRequest.responses, bulkRequest.errors
}

func (cl *BulkClient) completionListener(bulkRequest *RoundTrip, collectResponses chan []roundTripParcel) {
	responses := <-collectResponses
	for _, resParcel := range responses {
		if resParcel.err != nil {
			bulkRequest.updateErrorForIndex(resParcel.err, resParcel.index)
		} else {
			bulkRequest.updateResponseForIndex(resParcel.response, resParcel.index)
		}
	}

	close(collectResponses)
	bulkRequest.addRequestIgnoredErrors()
}

func (cl *BulkClient) responseMux(ctx context.Context,
	bulkRequest *RoundTrip,
	processedResponses <-chan roundTripParcel, collectResponses chan<- []roundTripParcel) {

	var arrayOfResponses []roundTripParcel
LOOP:
	for done := 0; done < len(bulkRequest.requests); {
		select {
		case <-ctx.Done():
			break LOOP

		case resParcel, isOpen := <-processedResponses:
			if isOpen {
				arrayOfResponses = append(arrayOfResponses, resParcel)
				done++
			} else {
				break LOOP
			}
		}

	}

	collectResponses <- arrayOfResponses
}

func (cl *BulkClient) workerManager(ctx context.Context, bulkRequest *RoundTrip, roundTripChannels *roundTripChannels, stopProcessing chan struct{}) {
	var publishWg, fireWg, processWg sync.WaitGroup

	publishWg.Add(1)
	go bulkRequest.publishAllRequests(roundTripChannels.requestList,
		stopProcessing,
		&publishWg)

	cl.fireRequestsManager(bulkRequest.fireRequestsWorkers,
		roundTripChannels.requestList,
		roundTripChannels.receivedResponses,
		stopProcessing,
		&fireWg)
	cl.processRequestsManager(ctx,
		bulkRequest.processResponseWorkers,
		roundTripChannels.receivedResponses,
		roundTripChannels.processedResponses,
		stopProcessing,
		&processWg)

	publishWg.Wait()
	close(roundTripChannels.requestList)

	fireWg.Wait()
	close(roundTripChannels.receivedResponses)

	processWg.Wait()
	close(roundTripChannels.processedResponses)
}

func (cl *BulkClient) fireRequestsManager(fireRequestsWorkers int,
	requestList <-chan requestParcel,
	recievedResponses chan<- roundTripParcel,
	stopProcessing <-chan struct{},
	fireWg *sync.WaitGroup) {

	for nWorker := 0; nWorker < fireRequestsWorkers; nWorker++ {
		fireWg.Add(1)
		go cl.fireRequests(requestList, recievedResponses, stopProcessing, fireWg)
	}

}

func (cl *BulkClient) processRequestsManager(ctx context.Context,
	processResponseWorkers int,
	recievedResponses <-chan roundTripParcel, processedResponses chan<- roundTripParcel,
	stopProcessing <-chan struct{}, processWg *sync.WaitGroup) {

	for mWorker := 0; mWorker < processResponseWorkers; mWorker++ {
		processWg.Add(1)
		go cl.processRequests(ctx, recievedResponses, processedResponses, stopProcessing, processWg)
	}

}

func (cl *BulkClient) fireRequests(reqList <-chan requestParcel,
	receivedResponses chan<- roundTripParcel,
	stopProcessing <-chan struct{},
	fireWg *sync.WaitGroup) {

LOOP:
	for reqParcel := range reqList {
		result := cl.executeRequest(reqParcel)
		select {
		case receivedResponses <- result:
		case <-stopProcessing:
			if result.response != nil {
				io.Copy(ioutil.Discard, result.response.Body)
				result.response.Body.Close()
			}
			break LOOP
		}
	}

	fireWg.Done()
}

func (cl *BulkClient) executeRequest(reqParcel requestParcel) roundTripParcel {
	resp, err := cl.httpclient.Do(reqParcel.request)

	return roundTripParcel{
		request:  reqParcel.request,
		response: resp,
		err:      err,
		index:    reqParcel.index,
	}
}

func (cl *BulkClient) processRequests(ctx context.Context,
	resList <-chan roundTripParcel,
	processedResponses chan<- roundTripParcel,
	stopProcessing <-chan struct{},
	processWg *sync.WaitGroup) {

LOOP:
	for resParcel := range resList {
		result := cl.parseResponse(ctx, resParcel)

		select {
		case processedResponses <- result:
		case <-stopProcessing:
			break LOOP
		}
	}

	processWg.Done()
}

// parseResponse and recreate a Response object with a new Request object (without a timeout).
// It is easy to read from the response object later after we're done processing all requests or we timeout.
// We do not want to be reading from a response for which the request has been canceled.
// We simply close the original response at the end of this function.
func (cl *BulkClient) parseResponse(ctx context.Context, res roundTripParcel) roundTripParcel {
	if res.response != nil {
		defer res.response.Body.Close()
	}

	if res.err != nil && (ctx.Err() == context.Canceled || ctx.Err() == context.DeadlineExceeded) {
		return roundTripParcel{err: ErrRequestIgnored, index: res.index}
	}

	if res.err != nil {
		return roundTripParcel{err: fmt.Errorf("http client error: %s", res.err), index: res.index}
	}

	if res.response == nil {
		return roundTripParcel{err: errors.New("no response received"), index: res.index}
	}

	bs, err := ioutil.ReadAll(res.response.Body)
	if err != nil {
		return roundTripParcel{err: fmt.Errorf("error while reading response body: %s", err), index: res.index}
	}

	body := ioutil.NopCloser(bytes.NewReader(bs))

	newResponse := http.Response{
		Body:       body,
		StatusCode: res.response.StatusCode,
		Status:     res.response.Status,
		Header:     res.response.Header,
		Request:    res.request.WithContext(context.Background()),
	}

	result := roundTripParcel{
		response: &newResponse,
		err:      err,
		index:    res.index,
	}

	return result
}
