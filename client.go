package bulkhttpclient

import (
	"net/http"
	"context"
	"time"
	"fmt"
	"errors"
	"io/ioutil"
	"bytes"
)

var RequestIgnored = errors.New("request ignored")
var NoRequests = errors.New("no requests provided")

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

type responseParcel struct {
	response *http.Response
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

//addResponse ...
func (r *RoundTrip) addResponse(response *http.Response, index int) *RoundTrip {
	r.responses[index] = response
	r.errors[index] = nil
	return r
}

//addErrors ...
func (r *RoundTrip) addError(err error, index int) *RoundTrip {
	r.errors[index] = err
	r.responses[index] = nil
	return r
}

//Do ...
func (cl *BulkClient) Do(bulkRequest *RoundTrip) ([]*http.Response, []error) {
	noOfRequests := len(bulkRequest.requests)
	if noOfRequests == 0 {
		return nil, []error{NoRequests}
	}

	bulkRequest.responses = make([]*http.Response, noOfRequests)
	bulkRequest.errors = make([]error, noOfRequests)

	ctx, cancel := context.WithTimeout(context.Background(), cl.timeout)
	defer cancel()

	results := make(chan responseParcel, noOfRequests)

	for i, req := range bulkRequest.requests {
		bulkRequest.requests[i] = req.WithContext(ctx)
		go cl.produce(ctx, bulkRequest.requests[i], results, i)
	}

	// TODO: Add a way to make multiple listeners
	return cl.listener(ctx, bulkRequest, cancel, results)
}

//CloseAllResponses ...
func (r *RoundTrip) CloseAllResponses() {
	for _, response := range r.responses {
		if response != nil {
			response.Body.Close()
		}
	}
}

func (r *RoundTrip) addRequestIgnoredErrors(doneRequests int) {
	for i, response := range r.responses {
		if response == nil && r.errors[i] == nil {
			r.errors[i] = RequestIgnored
		}
	}
}

func (cl *BulkClient) listener(ctx context.Context, bulkRequest *RoundTrip, cancelFunc context.CancelFunc, results chan responseParcel) ([]*http.Response, []error) {
	done := 0
	for {
		select {
		case <-ctx.Done():
			bulkRequest.addRequestIgnoredErrors(done)
			return bulkRequest.responses, bulkRequest.errors

		case resParcel := <-results:
			if resParcel.err != nil {
				bulkRequest.addError(resParcel.err, resParcel.index)
			} else {
				bulkRequest.addResponse(resParcel.response, resParcel.index)
			}

			done++

			if done == len(bulkRequest.requests) {
				return bulkRequest.responses, bulkRequest.errors
			}
		}
	}
}

func (cl *BulkClient) produce(ctx context.Context, req *http.Request, results chan responseParcel, index int) {
	resp, err := cl.httpclient.Do(req)

	defer func() {
		if resp != nil { resp.Body.Close() }
	}()

	if err != nil && (ctx.Err() == context.Canceled || ctx.Err() == context.DeadlineExceeded) {
		results <- responseParcel{err: RequestIgnored, index: index}
		return
	}

	if err != nil {
		results <- responseParcel{err: fmt.Errorf("http client error: %s", err), index: index}
		return
	}

	bs, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		results <- responseParcel{err: fmt.Errorf("error while reading response body: %s", err), index: index}
		return
	}

	body := ioutil.NopCloser(bytes.NewReader(bs))

	newResponse := http.Response{
		Body:       body,
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Header:     resp.Header,
		Request:    req.WithContext(context.Background()),
	}

	result := responseParcel{
		response: &newResponse,
		err:      err,
		index:    index,
	}

	if resp == nil {
		results <- responseParcel{err: errors.New("no response received"), index: index}
		return
	}

	results <- result
}
