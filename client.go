package bulkhttpclient

import (
	"net/http"
	"context"
	"time"
	"fmt"
	"errors"
//	"poi-service/httpclient"
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

//BulkRequest TODO: Change the name to something that represents a request-response cycle
type BulkRequest struct {
	requests   []*http.Request
	responses  []*http.Response
	errors     []error
}

type responseParcel struct {
	response *http.Response
	err      error
}

//NewBulkHTTPClient ...
func NewBulkHTTPClient(client HTTPClient, timeout time.Duration) *BulkClient {
	return &BulkClient{
		httpclient: client,
		timeout: timeout,
	}
}

//NewBulkRequest ...
func NewBulkRequest() *BulkRequest {
	return &BulkRequest{
		requests: []*http.Request{},
		responses: []*http.Response{},
	}
}

//Add ...
func (r *BulkRequest) Add(request *http.Request) *BulkRequest {
	r.requests = append(r.requests, request)
	return r
}

//addResponse ...
func (r *BulkRequest) addResponse(response *http.Response) *BulkRequest {
	r.responses = append(r.responses, response)
	return r
}


//addErrors ...
func (r *BulkRequest) addError(err error) *BulkRequest {
	r.errors = append(r.errors, err)
	return r
}

//Do ...
func (cl *BulkClient) Do(bulkRequest *BulkRequest) ([]*http.Response, []error) {
	noOfRequests := len(bulkRequest.requests)

	if noOfRequests == 0 {
		return nil, []error{errors.New("No requests provided")}
	}

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
func (r *BulkRequest) CloseAllResponses() {
	for _, response := range r.responses {
		response.Body.Close()
	}
}

func (cl *BulkClient) listener(ctx context.Context, bulkRequest *BulkRequest, cancelFunc context.CancelFunc, results chan responseParcel) ([]*http.Response, []error) {
	done := 0

        for {
		select {
		case <-ctx.Done():
			return bulkRequest.responses, bulkRequest.errors

		case ok := <-results:
			if ok.err != nil {
				bulkRequest.addError(ok.err)
			} else {
				bulkRequest.addResponse(ok.response)
			}

			done++

			if done == len(bulkRequest.requests) {
				return bulkRequest.responses, bulkRequest.errors
			}
		}
	}
}

func (cl *BulkClient) produce(ctx context.Context, req *http.Request, results chan responseParcel, i int) {
	resp, err := cl.httpclient.Do(req)

	if err != nil {
		fmt.Printf("\nHTTP request error'd : %v : %s\n", i, err)
		results <- responseParcel{err: err}
		return
	}

	result := responseParcel{
		response: resp,
		err:    err,
	}

	if resp == nil {
		results <- responseParcel{err: errors.New("No response received")}
		return
	}

	results <- result
}
