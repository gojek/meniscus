package meniscus

import (
	"errors"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"testing"
	"time"
)

const (
	MockServerSlowResponseSleep = 50 * time.Millisecond
	NonFailingTimeoutValue      = MockServerSlowResponseSleep + time.Second
	FailingTimeoutValue         = MockServerSlowResponseSleep - 40*time.Millisecond
)

func TestBulkHTTPClientExecutesRequestsConcurrentlyAndAllRequestsSucceed(t *testing.T) {
	server := StartMockServer()
	defer server.Close()
	noOfRequests := 10
	timeout := NonFailingTimeoutValue
	httpclient := &http.Client{Timeout: timeout}
	client := NewBulkHTTPClient(httpclient, timeout)
	var requests []*http.Request

	for i := 0; i < noOfRequests; i++ {
		query := url.Values{}
		query.Set("kind", "fast")
		req, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", query), nil)
		require.NoError(t, err, "no errors")
		requests = append(requests, req)
	}

	bulkRequest := NewBulkRequest(requests, 10, 10)

	responses, _ := client.Do(bulkRequest)

	assert.Equal(t, noOfRequests, len(responses))

	for _, resp := range responses {
		resByte, e := ioutil.ReadAll(resp.Body)

		assert.Equal(t, "fast", string(resByte))
		assert.Nil(t, e)
	}

	bulkRequest.CloseAllResponses()
}

func TestBulkHTTPClientReturnsResponsesInOrder(t *testing.T) {
	server := StartMockServer()
	defer server.Close()
	bulkClientTimeout := NonFailingTimeoutValue
	httpclient := &http.Client{Timeout: NonFailingTimeoutValue}
	client := NewBulkHTTPClient(httpclient, bulkClientTimeout)

	queryFast := url.Values{}
	queryFast.Set("kind", "fast")

	querySlow := url.Values{}
	querySlow.Set("kind", "slow")

	reqOne, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")

	reqTwo, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", queryFast), nil)
	require.NoError(t, err, "no errors")

	reqThree, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")

	bulkRequest := NewBulkRequest([]*http.Request{reqOne, reqTwo, reqThree}, 10, 10)

	responses, _ := client.Do(bulkRequest)
	responseOne, _ := ioutil.ReadAll(responses[0].Body)
	responseTwo, _ := ioutil.ReadAll(responses[1].Body)
	responseThree, _ := ioutil.ReadAll(responses[2].Body)

	assert.Equal(t, "slow", string(responseOne))
	assert.Equal(t, "fast", string(responseTwo))
	assert.Equal(t, "slow", string(responseThree))

	bulkRequest.CloseAllResponses()
}

func TestBulkHTTPClientAllRequestsFailDueToBulkClientContextTimeout(t *testing.T) {
	server := StartMockServer()
	defer server.Close()
	bulkClientTimeout := FailingTimeoutValue
	httpclient := &http.Client{Timeout: NonFailingTimeoutValue}
	client := NewBulkHTTPClient(httpclient, bulkClientTimeout)

	queryFast := url.Values{}
	queryFast.Set("kind", "fast")

	querySlow := url.Values{}
	querySlow.Set("kind", "slow")

	reqOne, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")

	reqTwo, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")

	reqThree, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", queryFast), nil)
	require.NoError(t, err, "no errors")

	bulkRequest := NewBulkRequest([]*http.Request{reqOne, reqTwo, reqThree}, 10, 10)
	responses, errors := client.Do(bulkRequest)

	assert.Nil(t, responses[0])
	assert.Equal(t, ErrRequestIgnored, errors[0])

	assert.Nil(t, responses[1])
	assert.Equal(t, ErrRequestIgnored, errors[1])

	assert.NotNil(t, responses[2])
	assert.Nil(t, errors[2])

	bulkRequest.CloseAllResponses()
}

func TestBulkHTTPClientAllRequestsFailDueToHTTPClientTimeout(t *testing.T) {
	server := StartMockServer()
	defer server.Close()
	bulkClientTimeout := NonFailingTimeoutValue
	httpclient := &http.Client{Timeout: FailingTimeoutValue}
	client := NewBulkHTTPClient(httpclient, bulkClientTimeout)

	queryFast := url.Values{}
	queryFast.Set("kind", "fast")

	querySlow := url.Values{}
	querySlow.Set("kind", "slow")

	reqOne, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")

	reqTwo, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")

	bulkRequest := NewBulkRequest([]*http.Request{reqOne, reqTwo}, 10, 10)
	responses, errs := client.Do(bulkRequest)

	expectedClientTimeoutError := fmt.Errorf("http client error: Get %s?kind=slow: net/http: request canceled (Client.Timeout exceeded while awaiting headers)", server.URL)

	assert.Equal(t, []*http.Response{nil, nil}, responses)
	for _, e := range errs {
		assert.Equal(t, expectedClientTimeoutError, e)
	}

	bulkRequest.CloseAllResponses()
}

func TestBulkHTTPClientSomeRequestsTimeoutAndOthersSucceedOrFailWithManyRequestWorkers(t *testing.T) {
	server := StartMockServer()
	defer server.Close()
	bulkClientTimeout := FailingTimeoutValue
	httpclient := &http.Client{Timeout: NonFailingTimeoutValue}
	client := NewBulkHTTPClient(httpclient, bulkClientTimeout)

	queryFast := url.Values{}
	queryFast.Set("kind", "fast")

	querySlow := url.Values{}
	querySlow.Set("kind", "slow")

	reqOne, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil) // bulk client timeout exceeded
	require.NoError(t, err, "no errors")

	reqTwo, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", queryFast), nil) // success
	require.NoError(t, err, "no errors")

	reqThree, err := http.NewRequest(http.MethodGet, server.URL, nil) // http client error failure
	require.NoError(t, err, "no errors")
	reqThree.URL = nil

	reqFour, err := http.NewRequest(http.MethodGet, server.URL, nil) // http client error failure
	require.NoError(t, err, "no errors")
	reqFour.URL = nil

	bulkRequest := NewBulkRequest([]*http.Request{reqOne, reqTwo, reqThree, reqFour}, 2, 2)
	responses, errs := client.Do(bulkRequest)
	defer bulkRequest.CloseAllResponses()

	assert.Equal(t, 4, len(responses))
	successResponse, _ := ioutil.ReadAll(responses[1].Body)

	assert.Equal(t, "fast", string(successResponse))
	assert.Equal(t, ErrRequestIgnored, errs[0])
	assert.Equal(t, errors.New("http client error: http: nil Request.URL"), errs[2])
	assert.Equal(t, errors.New("http client error: http: nil Request.URL"), errs[3])
}

func TestBulkHTTPClientSomeRequestsTimeoutAndOthersSucceedOrFailWithOneRequestWorker(t *testing.T) {
	server := StartMockServer()
	defer server.Close()
	bulkClientTimeout := FailingTimeoutValue
	httpclient := &http.Client{Timeout: NonFailingTimeoutValue}
	client := NewBulkHTTPClient(httpclient, bulkClientTimeout)

	queryFast := url.Values{}
	queryFast.Set("kind", "fast")

	querySlow := url.Values{}
	querySlow.Set("kind", "slow")

	reqOne, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)  // bulk client timeout exceeded
	require.NoError(t, err, "no errors")

	reqTwo, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", queryFast), nil)  // success
	require.NoError(t, err, "no errors")

	reqThree, err := http.NewRequest(http.MethodGet, server.URL, nil)// http client error failure
	require.NoError(t, err, "no errors")
	reqThree.URL = nil

	reqFour, err := http.NewRequest(http.MethodGet, server.URL, nil)  // http client error failure
	require.NoError(t, err, "no errors")
	reqFour.URL = nil

	bulkRequest := NewBulkRequest([]*http.Request{reqOne, reqTwo, reqThree, reqFour}, 1, 1)
	_, errs := client.Do(bulkRequest)
	defer bulkRequest.CloseAllResponses()

	assert.Equal(t, ErrRequestIgnored, errs[0])
	assert.Equal(t, ErrRequestIgnored, errs[1])
	assert.Equal(t, ErrRequestIgnored, errs[2])
	assert.Equal(t, ErrRequestIgnored, errs[3])
}

func TestBulkClientRequestFirerAndProcessorGoroutinesAreClosed(t *testing.T) {
	server := StartMockServer()
	defer server.Close()
	timeout := NonFailingTimeoutValue
	httpclient := &http.Client{Timeout: timeout}
	totalBulkRequests := 50
	reqsPerBulkRequest := 5
	bulkRequestsDone := 0
	var responses []*http.Response
	var errs []error

	for noOfBulkRequests := 0; noOfBulkRequests < totalBulkRequests; noOfBulkRequests++ {
		client := NewBulkHTTPClient(httpclient, timeout)
		bulkRequest := newBulkClientWithNRequests(reqsPerBulkRequest, server.URL)
		res, err := client.Do(bulkRequest)
		responses = append(responses, res...)
		errs = append(errs, err...)
		bulkRequestsDone = bulkRequestsDone + 1
		bulkRequest.CloseAllResponses()
	}

	assert.Equal(t, totalBulkRequests, bulkRequestsDone)
	assert.Equal(t, totalBulkRequests*reqsPerBulkRequest, len(responses))
	assert.Equal(t, totalBulkRequests*reqsPerBulkRequest, len(errs))

	isLessThan50 := func(x int) bool {
		if x < 50 {
			return true
		}

		fmt.Printf("CAUSE OF FAILURE: %d is greater than 20\n", x)
		return false
	}

	assert.True(t, isLessThan50(runtime.NumGoroutine()))
}

func newBulkClientWithNRequests(n int, serverURL string) *RoundTrip {
	var requests []*http.Request
	for i := 0; i < n; i++ {
		query := url.Values{}
		req, _ := http.NewRequest(http.MethodGet, encodeURL(serverURL, "", query), nil)
		requests = append(requests, req)
	}

	bulkRequest := NewBulkRequest(requests, 10, 10)
	return bulkRequest
}
func StartMockServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(slowOrFastServerHandler))
}

func slowOrFastServerHandler(w http.ResponseWriter, req *http.Request) {
	slowOrFast := req.URL.Query().Get("kind")
	if len(slowOrFast) == 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	if slowOrFast == "slow" {
		time.Sleep(MockServerSlowResponseSleep)
		w.Write([]byte(slowOrFast))
		return
	}

	w.Write([]byte(slowOrFast))
	return
}

func encodeURL(baseURL string, endpoint string, queryParams url.Values) string {
	return fmt.Sprintf("%s%s?%s", baseURL, endpoint, queryParams.Encode())
}
