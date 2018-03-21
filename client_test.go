package bulkhttpclient

import (
	"testing"
	"net/http"
	"time"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"fmt"
)

func TestBulkHTTPClientExecutesRequestsConcurrently(t *testing.T) {
	server := StartMockServer()
	timeout := 2*time.Second
	httpclient := &http.Client{Timeout: timeout}
	client := NewBulkHTTPClient(httpclient, timeout)
	bulkRequest := NewBulkRequest()

	for i := 0; i < 3; i++ {
		req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:1111?kind=fast", nil)
		require.NoError(t, err, "no errors")
		bulkRequest.Add(req)
	}

	responses, _ := client.Do(bulkRequest)
	for _, resp := range responses {
		resByte, e := ioutil.ReadAll(resp.Body)

		assert.Equal(t, "fast", string(resByte))
		assert.Nil(t, e)
	}

	bulkRequest.CloseAllResponses()
	server.Shutdown(nil)
}


func StartMockServer() *http.Server {
	srv := &http.Server{Addr: ":1111"}
	http.HandleFunc("/", slowOrFastServerHandler)
	go srv.ListenAndServe()
	return srv
}

func slowOrFastServerHandler(w http.ResponseWriter, req *http.Request) {
	slowOrFast := req.URL.Query().Get("kind")

	if slowOrFast == "slow" {
		time.Sleep(1 * time.Second)
		fmt.Fprintf(w, slowOrFast)
		return
	}

	fmt.Fprintf(w, slowOrFast)
	return
}
