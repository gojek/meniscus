package bulkhttpclient

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	metrics "github.com/tevjef/go-runtime-metrics"
	_ "github.com/tevjef/go-runtime-metrics/expvar"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
	"sync/atomic"
)

func CollectMetrics() {
	sock, e := net.Listen("tcp", "localhost:6060")
	if e != nil {
		fmt.Printf("Could not start the metrics server %+v\n", e)
	}

	go func() {
		fmt.Println("TCP Metrics Collector Server now available at port 6060")
		http.Serve(sock, nil)
	}()

	err := metrics.RunCollector(metrics.DefaultConfig)
	if err != nil {
		fmt.Printf("Could not start the metrics collector %+v\n", err)
	}
}

func TestMain(m *testing.M) {
	CollectMetrics()
	os.Exit(m.Run())

}

func input() string {
	in := os.Stdin
	fmt.Print("Enter q to exit: ")
	var userInput string
	_, err := fmt.Fscanf(in, "%s", &userInput)
	if err != nil {
		panic(err)
	}

	return userInput
}

func TestBulkClientRuntimeMetrics(t *testing.T) {
	quit := make(chan struct{})
	fireRequestsAtFrequency(t, quit)
}

func fireRequestsAtFrequency(t *testing.T, quit <-chan struct{}) {
	requests := getIntFromEnvOrPanic("REQUESTS")
	requestSize := getIntFromEnvOrPanic("REQUEST_SIZE")
	timePeriodInt := getIntFromEnvOrPanic("TIME_INTERVAL_IN_MS")
	timePeriod := time.Duration(timePeriodInt) * time.Millisecond

	server := StartSleepingMockServer(20 * time.Millisecond)
	defer server.Close()

	ticker := time.NewTicker(timePeriod)

	var wg sync.WaitGroup
	var bulkRequestsDone uint64
	var responses []*http.Response
	var errs []error

	for {
		select {
		case <-ticker.C:
			work(requests, requestSize, timePeriod, bulkRequestsDone, responses, errs, server.URL, &wg)
			wg.Wait()
			assertions(t, requests, requestSize, bulkRequestsDone, responses, errs)

		case <-quit:
			ticker.Stop()
			return
		}
	}
}

func work(requests int, requestSize int, timePeriod time.Duration, bulkRequestsDone uint64, responses []*http.Response, errs []error, url string, wg *sync.WaitGroup) {
	fmt.Printf("Firing %d requests of size %d with an interval of %f seconds.\n", requests, requestSize, timePeriod.Seconds())
	timeout := 100 * time.Millisecond
	httpclient := &http.Client{Timeout: timeout}

	for noOfBulkRequests := 0; noOfBulkRequests < requests; noOfBulkRequests++ {
		wg.Add(1)
		go func() {
			client := NewBulkHTTPClient(httpclient, timeout)
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

func assertions(t *testing.T, requests int, requestSize int, bulkRequestsDone uint64, responses []*http.Response, errs []error) {
	assert.Equal(t, requests, int(bulkRequestsDone))
	assert.Equal(t, requests * requestSize, len(responses))
	assert.Equal(t, requests * requestSize, len(errs))
}

func getIntFromEnvOrPanic(envVarName string) int {
	val, err := strconv.ParseInt(os.Getenv(envVarName), 10, 32)
	if err != nil {
		panic("Invalid value for env var" + envVarName)
	}
	return int(val)
}

func StartSleepingMockServer(sleepDuration time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(newSleepingHandler(sleepDuration)))
}

func newSleepingHandler(sleepDuration time.Duration) func(w http.ResponseWriter, req *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		time.Sleep(sleepDuration)
		w.Write([]byte("awake"))
		return
	}
}
