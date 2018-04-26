package perftest

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	metrics "github.com/tevjef/go-runtime-metrics"
	_ "github.com/tevjef/go-runtime-metrics/expvar"
	"net"
	"net/http"
	"os"
	"testing"
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

func TestBulkClientRuntimeMetrics(t *testing.T) {
	quit := make(chan struct{})
	fireRequestsAtFrequency(t, quit)
}

func assertions(t *testing.T, requests int, requestSize int, bulkRequestsDone uint64, responses []*http.Response, errs []error) {
	assert.Equal(t, requests, int(bulkRequestsDone))
	assert.Equal(t, requests * requestSize, len(responses))
	assert.Equal(t, requests * requestSize, len(errs))
}
