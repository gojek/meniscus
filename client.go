package meniscus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"
	"time"
)

//HTTPClient ...
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

//Request ..
type Request interface {
	Add(*http.Request) Request
}

//BulkClient ...
type BulkClient struct {
	httpclient HTTPClient
	timeout    time.Duration
}

//RoundTrip ...
type RoundTrip struct {
	requests  []*http.Request
	responses []*http.Response
	errors    []error
}

//ErrNoRequests ...
var ErrNoRequests = errors.New("no requests provided")

//ErrRequestIgnored ...
var ErrRequestIgnored = errors.New("request ignored")

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

//NewBulkRequest ...
func NewBulkRequest() *RoundTrip {
	return &RoundTrip{
		requests:  []*http.Request{},
		responses: []*http.Response{},
	}
}

//AddRequest ...
func (r *RoundTrip) AddRequest(request *http.Request) *RoundTrip {
	r.requests = append(r.requests, request)
	return r
}

//Do ...
func (cl *BulkClient) Do(bulkRequest *RoundTrip, fireRequestsWorkers int, processResponseWorkers int) ([]*http.Response, []error) {
	noOfRequests := len(bulkRequest.requests)
	if noOfRequests == 0 {
		return nil, []error{ErrNoRequests}
	}

	bulkRequest.responses = make([]*http.Response, noOfRequests)
	bulkRequest.errors = make([]error, noOfRequests)

	requestList := make(chan requestParcel)
	recievedResponses := make(chan roundTripParcel)
	processedResponses := make(chan roundTripParcel)
	collectResponses := make(chan []roundTripParcel)
	stopProcessing := make(chan struct{})
	defer close(stopProcessing)

	ctx, cancel := context.WithTimeout(context.Background(), cl.timeout)
	defer cancel()

	for index, req := range bulkRequest.requests {
		bulkRequest.requests[index] = req.WithContext(ctx)
	}

	go cl.responseMux(ctx, bulkRequest, processedResponses, collectResponses)
	go cl.workerManager(ctx, bulkRequest, fireRequestsWorkers, processResponseWorkers, requestList, recievedResponses, processedResponses, stopProcessing)

	cl.completionListener(bulkRequest, collectResponses)

	return bulkRequest.responses, bulkRequest.errors
}

//CloseAllResponses ...
func (r *RoundTrip) CloseAllResponses() {
	for _, response := range r.responses {
		if response != nil {
			response.Body.Close()
		}
	}
}

func (r *RoundTrip) publishAllRequests(requestList chan<- requestParcel, stopProcessing <-chan struct{}, publishWg *sync.WaitGroup) {
LOOP:
	for index := range r.requests {
		reqParcel := requestParcel{
			request: r.requests[index],
			index:   index,
		}

		select {
		case requestList <- reqParcel:
		case <-stopProcessing:
			break LOOP
		}
	}

	publishWg.Done()
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

func (r *RoundTrip) addRequestIgnoredErrors() {
	for i, response := range r.responses {
		if response == nil && r.errors[i] == nil {
			r.errors[i] = ErrRequestIgnored
		}
	}
}

func (r *RoundTrip) updateResponseForIndex(response *http.Response, index int) *RoundTrip {
	r.responses[index] = response
	r.errors[index] = nil
	return r
}

func (r *RoundTrip) updateErrorForIndex(err error, index int) *RoundTrip {
	r.errors[index] = err
	r.responses[index] = nil
	return r
}

func (cl *BulkClient) workerManager(ctx context.Context,
	bulkRequest *RoundTrip,
	fireRequestsWorkers int, processResponseWorkers int,
	requestList chan requestParcel,
	recievedResponses chan roundTripParcel, processedResponses chan roundTripParcel,
	stopProcessing chan struct{}) {

	var publishWg, fireWg, processWg sync.WaitGroup

	publishWg.Add(1)
	go bulkRequest.publishAllRequests(requestList, stopProcessing, &publishWg)
	cl.fireRequestsManager(fireRequestsWorkers, requestList, recievedResponses, stopProcessing, &fireWg)
	cl.processRequestsManager(ctx, processResponseWorkers, recievedResponses, processedResponses, stopProcessing, &processWg)

	publishWg.Wait()
	close(requestList)

	fireWg.Wait()
	close(recievedResponses)

	processWg.Wait()
	close(processedResponses)
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
			if result.response != nil { result.response.Body.Close() }
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
	defer func() {
		if res.response != nil {
			res.response.Body.Close()
		}
	}()

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
