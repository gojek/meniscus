package meniscus

import (
	"net/http"
	"sync"
)

//Request ..
type Request interface {
	Add(*http.Request) Request
}

//RoundTrip ...
type RoundTrip struct {
	requests               []*http.Request
	fireRequestsWorkers    int
	responses              []*http.Response
	processResponseWorkers int
	errors                 []error
}

//NewBulkRequest ...
func NewBulkRequest(requests []*http.Request, fireRequestsWorkers int, processResponseWorkers int) *RoundTrip {
	return &RoundTrip{
		requests:               requests,
		fireRequestsWorkers:    fireRequestsWorkers,
		responses:              []*http.Response{},
		processResponseWorkers: processResponseWorkers,
	}
}

//AddRequest ...
func (r *RoundTrip) AddRequest(request *http.Request) *RoundTrip {
	r.requests = append(r.requests, request)
	return r
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
