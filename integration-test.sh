#!/bin/bash

set -e

#####################################################################################
# Test the grok_exporter executable in $GOPATH/bin
#####################################################################################

# Mock cygpath on Linux and OS X, so we can run the same script on all operating systems.
if [[ $(uname -s | tr '[a-z]' '[A-Z]') != *"CYGWIN"* ]] ; then
    function cygpath() {
        echo $2
    }
fi

config_file=$(mktemp /tmp/grok_exporter-test-config.XXXXXX)
log_file=$(mktemp /tmp/grok_exporter-test-log.XXXXXX)

function cleanup_temp_files {
    echo "cleaning up..."
    rm -f $config_file
    rm -f $log_file
}

# clean up on exit
trap cleanup_temp_files EXIT

cat <<EOF > $config_file
global:
    config_version: 2
input:
    type: file
    path: $(cygpath -w $log_file)
    readall: true
grok:
    patterns_dir: $(cygpath -w $GOPATH/src/github.com/fstab/grok_exporter/logstash-patterns-core/patterns)
    additional_patterns:
    - 'EXIM_MESSAGE [a-zA-Z ]*'
metrics:
    - type: counter
      name: grok_test_counter_nolabels
      help: Counter metric without labels.
      match: '%{DATE} %{TIME} %{USER:user} %{NUMBER:val}'
    - type: counter
      name: grok_test_counter_labels
      help: Counter metric with labels.
      match: '%{DATE} %{TIME} %{USER:user} %{NUMBER:val}'
      labels:
          user: '{{.user}}'
    - type: gauge
      name: grok_test_gauge_nolabels
      help: Gauge metric without labels.
      match: '%{DATE} %{TIME} %{USER:user} %{NUMBER:val}'
      value: '{{.val}}'
    - type: gauge
      name: grok_test_gauge_labels
      help: Gauge metric with labels.
      match: '%{DATE} %{TIME} %{USER:user} %{NUMBER:val}'
      value: '{{.val}}'
      labels:
          user: '{{.user}}'
    - type: histogram
      name: grok_test_histogram_nolabels
      help: Histogram metric without labels.
      match: '%{DATE} %{TIME} %{USER:user} %{NUMBER:val}'
      value: '{{.val}}'
      buckets: [1, 2, 3]
    - type: histogram
      name: grok_test_histogram_labels
      help: Histogram metric with labels.
      match: '%{DATE} %{TIME} %{USER:user} %{NUMBER:val}'
      value: '{{.val}}'
      buckets: [1, 2, 3]
      labels:
          user: '{{.user}}'
    - type: summary
      name: grok_test_summary_nolabels
      help: Summary metric without labels.
      match: '%{DATE} %{TIME} %{USER:user} %{NUMBER:val}'
      quantiles: {0.5: 0.05, 0.9: 0.01, 0.99: 0.001}
      value: '{{.val}}'
    - type: summary
      name: grok_test_summary_labels
      help: Summary metric with labels.
      match: '%{DATE} %{TIME} %{USER:user} %{NUMBER:val}'
      value: '{{.val}}'
      quantiles: {0.5: 0.05, 0.9: 0.01, 0.99: 0.001}
      labels:
          user: '{{.user}}'
server:
    port: 9144
EOF

touch $log_file

$GOPATH/bin/grok_exporter -config $(cygpath -w $config_file) &
pid=$!
disown
trap "kill $pid && cleanup_temp_files" EXIT
sleep 0.25

echo '30.07.2016 14:37:03 alice 1.5' >> $log_file
echo 'some unrelated line' >> $log_file
echo '30.07.2016 14:37:33 alice 2.5' >> $log_file
echo '30.07.2016 14:43:02 bob 2.5' >> $log_file

function checkMetric() {
    # escaping backslashes is only relevant for Windows
    escaped=$(echo $1 | sed 's,\\,\\\\,g')
    val=$(curl -s http://localhost:9144/metrics | grep -v '#' | grep "$escaped ") || ( echo "FAILED: Metric '$1' not found." >&2 && exit -1 )
    echo $val | grep "$escaped $2" > /dev/null || ( echo "FAILED: Expected '$1 $2', but got '$val'." >&2 && exit -1 )
}

# escaping backslashes is only relevant for Windows
input=$(cygpath -w $log_file | sed 's,\\,\\\\,g')

echo "Checking metrics..."

checkMetric "grok_test_counter_nolabels{input=\"$input\"}" 3
checkMetric "grok_test_counter_labels{input=\"$input\",user=\"alice\"}" 2
checkMetric "grok_test_counter_labels{input=\"$input\",user=\"bob\"}" 1

checkMetric "grok_test_gauge_nolabels{input=\"$input\"}" 2.5
checkMetric "grok_test_gauge_labels{input=\"$input\",user=\"alice\"}" 2.5
checkMetric "grok_test_gauge_labels{input=\"$input\",user=\"bob\"}" 2.5

checkMetric "grok_test_histogram_nolabels_bucket{input=\"$input\",le=\"1\"}" 0
checkMetric "grok_test_histogram_nolabels_bucket{input=\"$input\",le=\"2\"}" 1
checkMetric "grok_test_histogram_nolabels_bucket{input=\"$input\",le=\"3\"}" 3
checkMetric "grok_test_histogram_nolabels_bucket{input=\"$input\",le=\"+Inf\"}" 3
checkMetric "grok_test_histogram_nolabels_sum{input=\"$input\"}" 6.5
checkMetric "grok_test_histogram_nolabels_count{input=\"$input\"}" 3

checkMetric "grok_test_histogram_labels_bucket{input=\"$input\",user=\"alice\",le=\"1\"}" 0
checkMetric "grok_test_histogram_labels_bucket{input=\"$input\",user=\"alice\",le=\"2\"}" 1
checkMetric "grok_test_histogram_labels_bucket{input=\"$input\",user=\"alice\",le=\"3\"}" 2
checkMetric "grok_test_histogram_labels_bucket{input=\"$input\",user=\"alice\",le=\"+Inf\"}" 2
checkMetric "grok_test_histogram_labels_sum{input=\"$input\",user=\"alice\"}" 4
checkMetric "grok_test_histogram_labels_count{input=\"$input\",user=\"alice\"}" 2

checkMetric "grok_test_histogram_labels_bucket{input=\"$input\",user=\"bob\",le=\"1\"}" 0
checkMetric "grok_test_histogram_labels_bucket{input=\"$input\",user=\"bob\",le=\"2\"}" 0
checkMetric "grok_test_histogram_labels_bucket{input=\"$input\",user=\"bob\",le=\"3\"}" 1
checkMetric "grok_test_histogram_labels_bucket{input=\"$input\",user=\"bob\",le=\"+Inf\"}" 1
checkMetric "grok_test_histogram_labels_sum{input=\"$input\",user=\"bob\"}" 2.5
checkMetric "grok_test_histogram_labels_count{input=\"$input\",user=\"bob\"}" 1

checkMetric "grok_test_summary_nolabels{input=\"$input\",quantile=\"0.9\"}" 2.5
checkMetric "grok_test_summary_nolabels_sum{input=\"$input\"}" 6.5
checkMetric "grok_test_summary_nolabels_count{input=\"$input\"}" 3

checkMetric "grok_test_summary_labels{input=\"$input\",user=\"alice\",quantile=\"0.9\"}" 2.5
checkMetric "grok_test_summary_labels_sum{input=\"$input\",user=\"alice\"}" 4
checkMetric "grok_test_summary_labels_count{input=\"$input\",user=\"alice\"}" 2

checkMetric "grok_test_summary_labels{input=\"$input\",user=\"bob\",quantile=\"0.9\"}" 2.5
checkMetric "grok_test_summary_labels_sum{input=\"$input\",user=\"bob\"}" 2.5
checkMetric "grok_test_summary_labels_count{input=\"$input\",user=\"bob\"}" 1

# Check built-in metrics (except for processing time and buffer load):

checkMetric "grok_exporter_lines_total{input=\"$input\",status=\"ignored\"}" 1
checkMetric "grok_exporter_lines_total{input=\"$input\",status=\"matched\"}" 3

checkMetric "grok_exporter_lines_matching_total{input=\"$input\",metric=\"grok_test_counter_labels\"}" 3
checkMetric "grok_exporter_lines_matching_total{input=\"$input\",metric=\"grok_test_counter_nolabels\"}" 3
checkMetric "grok_exporter_lines_matching_total{input=\"$input\",metric=\"grok_test_gauge_labels\"}" 3
checkMetric "grok_exporter_lines_matching_total{input=\"$input\",metric=\"grok_test_gauge_nolabels\"}" 3
checkMetric "grok_exporter_lines_matching_total{input=\"$input\",metric=\"grok_test_histogram_labels\"}" 3
checkMetric "grok_exporter_lines_matching_total{input=\"$input\",metric=\"grok_test_histogram_nolabels\"}" 3
checkMetric "grok_exporter_lines_matching_total{input=\"$input\",metric=\"grok_test_summary_labels\"}" 3
checkMetric "grok_exporter_lines_matching_total{input=\"$input\",metric=\"grok_test_summary_nolabels\"}" 3

checkMetric "grok_exporter_line_processing_errors_total{input=\"$input\",metric=\"grok_test_counter_labels\"}" 0
checkMetric "grok_exporter_line_processing_errors_total{input=\"$input\",metric=\"grok_test_counter_nolabels\"}" 0
checkMetric "grok_exporter_line_processing_errors_total{input=\"$input\",metric=\"grok_test_gauge_labels\"}" 0
checkMetric "grok_exporter_line_processing_errors_total{input=\"$input\",metric=\"grok_test_gauge_nolabels\"}" 0
checkMetric "grok_exporter_line_processing_errors_total{input=\"$input\",metric=\"grok_test_histogram_labels\"}" 0
checkMetric "grok_exporter_line_processing_errors_total{input=\"$input\",metric=\"grok_test_histogram_nolabels\"}" 0
checkMetric "grok_exporter_line_processing_errors_total{input=\"$input\",metric=\"grok_test_summary_labels\"}" 0
checkMetric "grok_exporter_line_processing_errors_total{input=\"$input\",metric=\"grok_test_summary_nolabels\"}" 0

rm $log_file
echo '30.07.2016 14:45:59 alice 2.5' >> $log_file

sleep 0.1
echo "Checking metrics..."

checkMetric "grok_test_counter_nolabels{input=\"$input\"}" 4

echo SUCCESS
