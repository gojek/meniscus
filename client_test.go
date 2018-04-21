package bulkhttpclient

import (
	"testing"
	"net/http"
	"time"
	"io/ioutil"
	"net/http/httptest"
	"net/url"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/assert"
)

const (
	MockServerSlowResponseSleep = 50 * time.Millisecond
	NonFailingTimeoutValue      = MockServerSlowResponseSleep + time.Second
	FailingTimeoutValue         = MockServerSlowResponseSleep - 40*time.Millisecond
)

func TestBulkHTTPClientExecutesRequestsConcurrentlyAndAllRequestsSucceed(t *testing.T) {
	server := StartMockServer()
	defer server.Close()
	timeout := NonFailingTimeoutValue
	httpclient := &http.Client{Timeout: timeout}
	client := NewBulkHTTPClient(httpclient, timeout)
	bulkRequest := NewBulkRequest()

	for i := 0; i < 3; i++ {
		query := url.Values{}
		query.Set("kind", "fast")
		req, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", query), nil)
		require.NoError(t, err, "no errors")
		bulkRequest.AddRequest(req)
	}

	responses, _ := client.Do(bulkRequest, 10, 10)

	assert.Equal(t, 3, len(responses))

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
	bulkRequest := NewBulkRequest()

	queryFast := url.Values{}
	queryFast.Set("kind", "fast")

	querySlow := url.Values{}
	querySlow.Set("kind", "slow")

	reqOne, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")
	bulkRequest.AddRequest(reqOne)

	reqTwo, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", queryFast), nil)
	require.NoError(t, err, "no errors")
	bulkRequest.AddRequest(reqTwo)

	reqThree, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")
	bulkRequest.AddRequest(reqThree)

	responses, _ := client.Do(bulkRequest, 10, 10)
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
	bulkRequest := NewBulkRequest()

	queryFast := url.Values{}
	queryFast.Set("kind", "fast")

	querySlow := url.Values{}
	querySlow.Set("kind", "slow")

	reqOne, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")
	bulkRequest.AddRequest(reqOne)

	reqTwo, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")
	bulkRequest.AddRequest(reqTwo)

	reqThree, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", queryFast), nil)
	require.NoError(t, err, "no errors")
	bulkRequest.AddRequest(reqThree)

	responses, errors := client.Do(bulkRequest, 10, 10)

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
	bulkRequest := NewBulkRequest()

	queryFast := url.Values{}
	queryFast.Set("kind", "fast")

	querySlow := url.Values{}
	querySlow.Set("kind", "slow")

	reqOne, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")
	bulkRequest.AddRequest(reqOne)

	reqTwo, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")
	bulkRequest.AddRequest(reqTwo)

	responses, errs := client.Do(bulkRequest, 10, 10)

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
	bulkRequest := NewBulkRequest()

	queryFast := url.Values{}
	queryFast.Set("kind", "fast")

	querySlow := url.Values{}
	querySlow.Set("kind", "slow")

	reqOne, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")
	bulkRequest.AddRequest(reqOne) // bulk client timeout exceeded

	reqTwo, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", queryFast), nil)
	require.NoError(t, err, "no errors")
	bulkRequest.AddRequest(reqTwo) // success

	reqThree, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err, "no errors")
	reqThree.URL = nil
	bulkRequest.AddRequest(reqThree) // http client error failure

	reqFour, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err, "no errors")
	reqFour.URL = nil
	bulkRequest.AddRequest(reqFour) // http client error failure

	responses, errs := client.Do(bulkRequest, 2, 2)
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
	bulkRequest := NewBulkRequest()

	queryFast := url.Values{}
	queryFast.Set("kind", "fast")

	querySlow := url.Values{}
	querySlow.Set("kind", "slow")

	reqOne, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")
	bulkRequest.AddRequest(reqOne) // bulk client timeout exceeded

	reqTwo, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", queryFast), nil)
	require.NoError(t, err, "no errors")
	bulkRequest.AddRequest(reqTwo) // success

	reqThree, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err, "no errors")
	reqThree.URL = nil
	bulkRequest.AddRequest(reqThree) // http client error failure

	reqFour, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err, "no errors")
	reqFour.URL = nil
	bulkRequest.AddRequest(reqFour) // http client error failure

	_, errs := client.Do(bulkRequest, 1, 1)
	defer bulkRequest.CloseAllResponses()

	assert.Equal(t, ErrRequestIgnored, errs[0])
	assert.Equal(t, ErrRequestIgnored, errs[1])
	assert.Equal(t, ErrRequestIgnored, errs[2])
	assert.Equal(t, ErrRequestIgnored, errs[3])
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
