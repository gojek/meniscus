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
setup-runtime-metrics:
	go get -d github.com/influxdata/telegraf
	make -C $(GOPATH)/src/github.com/influxdata/telegraf
	brew install grafana
	brew install influxdb
	@echo -e "${PURPLE}Please install this dashboard via the Grafana UI: https://grafana.com/dashboards/1144${NO_COLOR}"
start-metrics-listener:
	brew services start grafana
	brew services start influxdb
	$(GOPATH)/src/github.com/influxdata/telegraf/telegraf -config telegraf.conf
test-runtime:
	go test -run TestBulkClientRuntimeMetrics
