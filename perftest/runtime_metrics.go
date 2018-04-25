package perftest

import (
	"strconv"
	"time"
	"os"
	"sync/atomic"
	"net/http/httptest"
	"testing"
	"sync"
	"net/http"
	"meniscus"
	"fmt"
	"net/url"
	"log"
	_ "net/http/pprof"
)

var (
	logger = log.New(os.Stdout, "[PERF TEST LOG]: ", log.Ltime)
)

func fireRequestsAtFrequency(t *testing.T, quit <-chan struct{}) {
	StartProfilingServer()
	requests := getIntFromEnvOrPanic("REQUESTS")
	requestSize := getIntFromEnvOrPanic("REQUEST_SIZE")
	timePeriodInt := getIntFromEnvOrPanic("TIME_INTERVAL_IN_MS")
	timePeriod := time.Duration(timePeriodInt) * time.Millisecond
	iterations := getIntFromEnvOrPanic("ITERATIONS")

	server := StartSleepingMockServer(1 * time.Nanosecond)
	defer server.Close()

	ticker := time.NewTicker(timePeriod)
	var wg sync.WaitGroup
	var bulkRequestsDone uint64
	var responses []*http.Response
	var errs []error
	var iterationsCompleted int

	for {
		select {
		case <-ticker.C:
			if iterationsCompleted < iterations {
				work(requests, requestSize, timePeriod, bulkRequestsDone, responses, errs, server.URL, &wg)
				iterationsCompleted++
				wg.Wait()

				assertions(t, requests, requestSize, bulkRequestsDone, responses, errs)
			} else {
				logger.Println("Specified iterations are over.")
			}


		case <-quit:
			ticker.Stop()
			return
		}
	}
}

func work(requests int, requestSize int, timePeriod time.Duration, bulkRequestsDone uint64, responses []*http.Response, errs []error, url string, wg *sync.WaitGroup) {
	logger.Printf("Firing %d requests of size %d with an interval of %f seconds.\n", requests, requestSize, timePeriod.Seconds())
	timeout := 100 * time.Millisecond
	httpclient := &http.Client{Timeout: timeout}

	for noOfBulkRequests := 0; noOfBulkRequests < requests; noOfBulkRequests++ {
		wg.Add(1)
		go func() {
			client := meniscus.NewBulkHTTPClient(httpclient, timeout)
			bulkRequest := newBulkClientWithNRequests(requestSize, url)
			res, err := client.Do(bulkRequest, 10, 10)
			responses = append(responses, res...)
			errs = append(errs, err...)
			atomic.AddUint64(&bulkRequestsDone, 1)
			bulkRequest.CloseAllResponses()
			wg.Done()
		}()
	}
}

func getIntFromEnvOrPanic(envVarName string) int {
	val, err := strconv.ParseInt(os.Getenv(envVarName), 10, 32)
	if err != nil {
		panic("Invalid value for env var" + envVarName)
	}
	return int(val)
}

func StartProfilingServer() {
	go func() {
		logger.Printf("Profiling server available at: http://localhost:8080/debug/pprof")
		logger.Println(http.ListenAndServe("localhost:8080", nil))
	}()
}

func StartSleepingMockServer(sleepDuration time.Duration) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(newSleepingHandler(sleepDuration)))
	logger.Printf("Starting mock server at: %s \n", server.URL)
	return server
}

func newSleepingHandler(sleepDuration time.Duration) func(w http.ResponseWriter, req *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		time.Sleep(sleepDuration)
		w.Write([]byte("awake"))
		return
	}
}

func newBulkClientWithNRequests(n int, serverURL string) *meniscus.RoundTrip {
	bulkRequest := meniscus.NewBulkRequest()
	for i := 0; i < n; i++ {
		query := url.Values{}
		req, _ := http.NewRequest(http.MethodGet, encodeURL(serverURL, "", query), nil)
		bulkRequest.AddRequest(req)
	}
	return bulkRequest
}

func encodeURL(baseURL string, endpoint string, queryParams url.Values) string {
	return fmt.Sprintf("%s%s?%s", baseURL, endpoint, queryParams.Encode())
}
