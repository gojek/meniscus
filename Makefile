PROJECT_DIR=$(shell pwd)
PURPLE='\033[0;35m'
NO_COLOR='\033[0m'

setup:
	go get "github.com/tevjef/go-runtime-metrics"
	go get github.com/stretchr/testify/assert

test:
	go test client_test.go client.go

stress-test:
	go test client_test.go client.go -race -parallel 16 -cpu 1,2,4

runtime-test:
	REQUESTS=500 REQUEST_SIZE=5 TIME_INTERVAL_IN_MS=2000 ITERATIONS=30 go test perf-test/runtime_metrics_test.go perf-test/runtime_metrics.go -run TestBulkClientRuntimeMetrics -test.v

setup-runtime-test:
	go get -d github.com/influxdata/telegraf
	make -C $(GOPATH)/src/github.com/influxdata/telegraf
	brew install grafana
	brew install influxdb
	@echo -e "${PURPLE}Please install this dashboard via the Grafana UI: https://grafana.com/dashboards/1144${NO_COLOR}"

start-metrics-server:
	brew services start grafana
	brew services start influxdb
	$(GOPATH)/src/github.com/influxdata/telegraf/telegraf -config perf-test/telegraf.conf
