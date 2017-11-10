#!/bin/bash
# This script will graft the procfilter plugin sources in the original telegraf source tree.
# It can also be used to remove unwanted/unused plugins from the telegraf build.

if [[ -z "$GOPATH" ]]; then
    echo "This script assumes you have the GOPATH env variable set ($GOPATH/src is were 'go get' will install sources.)"
    exit 1
fi
Telegraf_src=$GOPATH/src/github.com/influxdata/telegraf
Plugins_src=$Telegraf_src/plugins/inputs/all/all.go

# New plugin (not in the standard distribution)
Add='	_ "github.com/influxdata/telegraf/plugins/inputs/procfilter" // Air France plugin.'

# Plugins to remove from the build. Considered harmful or waste of electrons on our servers.
#From $GOPATH/src/github.com/influxdata/telegraf/plugins/inputs/all
#	_ "github.com/influxdata/telegraf/plugins/inputs/jolokia"
Black_list='
	_ "github.com/influxdata/telegraf/plugins/inputs/aerospike"
	_ "github.com/influxdata/telegraf/plugins/inputs/chrony"
	_ "github.com/influxdata/telegraf/plugins/inputs/cloudwatch"
	_ "github.com/influxdata/telegraf/plugins/inputs/conntrack"
	_ "github.com/influxdata/telegraf/plugins/inputs/consul"
	_ "github.com/influxdata/telegraf/plugins/inputs/couchbase"
	_ "github.com/influxdata/telegraf/plugins/inputs/couchdb"
	_ "github.com/influxdata/telegraf/plugins/inputs/dns_query"
	_ "github.com/influxdata/telegraf/plugins/inputs/disque"
	_ "github.com/influxdata/telegraf/plugins/inputs/fluentd"
	_ "github.com/influxdata/telegraf/plugins/inputs/graylog"
	_ "github.com/influxdata/telegraf/plugins/inputs/kapacitor"
	_ "github.com/influxdata/telegraf/plugins/inputs/kubernetes"
	_ "github.com/influxdata/telegraf/plugins/inputs/leofs"
	_ "github.com/influxdata/telegraf/plugins/inputs/lustre2"
	_ "github.com/influxdata/telegraf/plugins/inputs/mailchimp"
	_ "github.com/influxdata/telegraf/plugins/inputs/mesos"
	_ "github.com/influxdata/telegraf/plugins/inputs/minecraft"
	_ "github.com/influxdata/telegraf/plugins/inputs/mqtt_consumer"
	_ "github.com/influxdata/telegraf/plugins/inputs/nats_consumer"
	_ "github.com/influxdata/telegraf/plugins/inputs/nsq"
	_ "github.com/influxdata/telegraf/plugins/inputs/nsq_consumer"
	_ "github.com/influxdata/telegraf/plugins/inputs/passenger"
	_ "github.com/influxdata/telegraf/plugins/inputs/phpfpm"
	_ "github.com/influxdata/telegraf/plugins/inputs/prometheus"
	_ "github.com/influxdata/telegraf/plugins/inputs/raindrops"
	_ "github.com/influxdata/telegraf/plugins/inputs/rethinkdb"
	_ "github.com/influxdata/telegraf/plugins/inputs/riak"
	_ "github.com/influxdata/telegraf/plugins/inputs/salesforce"
	_ "github.com/influxdata/telegraf/plugins/inputs/snmp_legacy"
	_ "github.com/influxdata/telegraf/plugins/inputs/sqlserver"
	_ "github.com/influxdata/telegraf/plugins/inputs/twemproxy"
	_ "github.com/influxdata/telegraf/plugins/inputs/udp_listener"
	_ "github.com/influxdata/telegraf/plugins/inputs/win_perf_counters"
	_ "github.com/influxdata/telegraf/plugins/inputs/win_services"
	_ "github.com/influxdata/telegraf/plugins/inputs/zfs"
	_ "github.com/influxdata/telegraf/plugins/inputs/zipkin"
	_ "github.com/influxdata/telegraf/plugins/inputs/zookeeper"
'

if [[ $1 =~ [-][^r] ]]; then
    # Usage.
    cat <<EOF
This script will graft the procfilter plugin sources in the original telegraf source tree.
-r: will also remove unwanted/unused plugins from the telegraf build. (see the source to edit the black list.)
EOF
fi

# Add local plugins.
echo "add procfilter"
sed -i "/.*Air France plugin.*/d" "$Plugins_src"
sed -i "s|)|$Add\n)|" "$Plugins_src"

if [[ $1 == "-r" ]]; then
    # Remove unwanted plugins.
    for p in $Black_list; do
	if [[ "$p" == "_" ]]; then
	    continue
	fi
	eval p=$p # removes the " around the plugin.
	echo "disable $p"
	sed -r -i 's|^([^/]*)('$p')(["].*)|//\1\2\3 // disabled|' "$Plugins_src"
    done
fi
