#!/bin/bash
#
# This script will graft the procfilter plugin sources in the original telegraf source tree.
# It can also be used to remove unwanted/unused plugins from the telegraf build.
# 

Telegraf_src=$GOPATH/src/github.com/influxdata/telegraf
Plugin_imports=$(find $Telegraf_src/plugins/ -name all.go)
Plugin_import_inputs=$(echo "$Plugin_imports" | grep inputs)

Tmp=/tmp/graft-plugin-$USER.tmp
Log=/tmp/graft-plugin-$USER.log
cat /dev/null > $Log
cat /dev/null > $Tmp
chmod a+rw $Log $Tmp

# Import procfilter plugin (not in the standard distribution)
Import_procfilter='	_ "github.com/influxdata/telegraf/plugins/inputs/procfilter" // New plugin.'

# Plugins to remove from the build. Considered harmful or waste of electrons on our servers.
# Using the -s option you get a list of plugins by size. It is then easy to remove the biggest and useless ones in your context.
# You can use a black list or a white list.

# Any plugin not in this white list will be disabled if -w is used.
# ls /etc/telegraf/afkl-conf/*inputs*conf| sed -r 's|inputs[.]|inputs/|;s|/etc/telegraf/afkl-conf/||' | cut -d. -f1 | sort -u
Plugins_white_list='
inputs/ceph
inputs/cpu
inputs/disk
inputs/diskio
inputs/exec
inputs/influxdb
inputs/libvirt
inputs/logparser
inputs/mem
inputs/processus
inputs/procfilter
inputs/swap
inputs/system
outputs/file
outputs/influxdb
inputs/postgresql
inputs/postgresql_extensible
inputs/mongodb
inputs/elasticsearch
inputs/haproxy
'

# Any plugin not in this white list will be disabled if -V is used.
# (white list for vmware dedicated telegraf ninary)
Plugins_vmware_white_list='
inputs/exec
inputs/vsphere
outputs/file
outputs/influxdb
'

# Windows context: Any plugin not in this white list will be disabled if -w is used.
Plugins_windows_white_list='
inputs/exec
inputs/win_perf_counters
processors/converter
processors/topk
outputs/file
outputs/influxdb
'


# These plugins will be disabled if using -b
Plugins_black_list='
inputs/aerospike
inputs/chrony
inputs/cloudwatch
inputs/conntrack
inputs/consul
inputs/couchbase
inputs/couchdb
inputs/dns_query
inputs/disque
inputs/fluentd
inputs/graylog
inputs/kapacitor
inputs/kubernetes
inputs/leofs
inputs/lustre2
inputs/mailchimp
inputs/mesos
inputs/minecraft
inputs/mqtt_consumer
inputs/nats_consumer
inputs/nsq
inputs/nsq_consumer
inputs/passenger
inputs/phpfpm
inputs/prometheus
inputs/raindrops
inputs/rethinkdb
inputs/riak
inputs/salesforce
inputs/snmp_legacy
inputs/sqlserver
inputs/twemproxy
inputs/udp_listener
inputs/win_perf_counters
inputs/win_services
inputs/zfs
inputs/zipkin
inputs/zookeeper
'


function usage(){
    cat <<EOF
Run this script to graft the procfilter plugin sources in the original telegraf source tree.
  -w: Remove unwanted/unused plugins that are not found in a white list from the telegraf build. (see the source to edit the white list.)
  -b: Remove unwanted/unused plugins found in a black list from the telegraf build. (see the source to edit the black list.)
  -V: Like -w but for a Vsphere dedicated binary.
  -W: Like -w but for a Windows dedicated binary.
  -s: display the size of all plugins (helps to select unwanted plugins).
EOF
}


function parse_opts(){
    while getopts "bwVWsh" opt; do
	case $opt in
	    b) Black=1;;
	    w) White=1;;
 	    V) Vsphere=1;;	    	        	    W) Windows=1;;	    	    W) Windows=1;;	    
	    s) Size=1;;
	    h) usage;
	       exit 0;;
	    *) usage
	       exit 1
	esac
    done

    # drop what has been parsed by getopts
    shift `expr $OPTIND - 1`
}

function save(){
    for i in $Plugin_imports; do
	if [[ ! -e $i.orig ]]; then
	    cp $i $i.orig # keep the original file before any change
	fi
	cp $i $i.save # keep the current file
    done
}

function restore(){
    for i in $Plugin_imports; do
	cp $i.orig $i
    done
}

# Comment the import line corresponding to a plugin.
# $1: plugin name (with the inputs/ or outputs/ part)
# eg: disable_plugin "inputs/powerdns"
function disable_plugin(){
    p="$1"
    for i in $Plugin_imports; do
	sed -r -i 's|^([^/]*)(github.com/influxdata/telegraf/plugins/'$p')(["].*)|//\1\2\3 // disabled by graft-plugin.sh|' "$i"
    done
}

function exec_size(){
    cd $Telegraf_src
    make telegraf >/dev/null 2>&1
    if [[ $? -eq 0 ]]; then
	Ret_size=$(cat telegraf|wc -c)
    else
	Ret_size=-1
    fi
}

function find_plugins(){
    Plugins=$(cat $Plugin_imports | egrep -v "^[[:space:]]*//" | egrep -o 'github.com/influxdata/telegraf/plugins/[^"]*'| sed -r 's|^.*github.com/influxdata/telegraf/plugins/||' | egrep -v "^[[:space:]]*$")
    Nb_plugins=$(echo -n "$Plugins" | wc -l)
}

# Use a black list of plugins to disable
function disable_plugins_bl(){
    local bl="$@"
    find_plugins

    # Remove unwanted plugins.
    # remove lines starting with // and erase part of the line like numbers and [] and filter out empty lines
    bl=$(echo "$bl" | egrep -v "[[:space:]]*//" | sed -r 's/^[[:space:]]*[0-9]+//g; s/[[][^]]*[]]//g' | egrep -v "^[[:space:]]*$")
    bl=$(echo "$bl" | sort -u)
    local nbl=$(echo -n $bl | wc -w)
    echo "Will disable $nbl blacklisted plugins (of $Nb_plugins total)."
    for p in $bl; do
	disable_plugin "$p"
	echo "  $p disabled"
    done
}


# Use a white list of plugins to allow and disable the rest.
function disable_plugins_wl(){
    local wl="$@"
    find_plugins
    # Remove unwanted plugins.
    # remove lines starting with // and erase part of the line like numbers and [] and filter out empty lines
    wl=$(echo "$wl" | egrep -v "[[:space:]]*//" | sed -r 's/^[[:space:]]*[0-9]+//g; s/[[][^]]*[]]//g' | egrep -v "^[[:space:]]*$")
    wl=$(echo "$wl" | sort -u)
    local nwl=$(echo $wl | wc -w)
    echo "$Plugins" > $Tmp
    for p in $wl; do
	grep -v "$p" $Tmp > $Tmp.2
	mv $Tmp.2 $Tmp
    done
    local bl=$(cat $Tmp)
    local nbl=$(echo -n "$bl" | wc -l)
    echo "Will disable $nbl plugins not in the $nwl whitelist  (of $Nb_plugins total)."
    #echo "wl: >>$wl<<"
    #echo "bl: >>$bl<<"
    for p in $bl; do
	disable_plugin "$p"
	echo "  $p disabled"
    done
}


function plugin_sizes(){
    save
    trap restore 1 2 3 11 15
    find_plugins
    echo "Computing size for $Nb_plugins plugins..."	  
    exec_size
    Full_size=$Ret_size
    (
	echo "$Full_size _telegraf_binary_with_all_plugins_ [0/$nbp]"
	i=1
	for p in $Plugins; do
	    restore
	    disable_plugin $p
	    exec_size
	    ps=$(($Full_size - $Ret_size))
	    echo "$ps $p [$i/$nbp]"
	    i=$((i+1))
	done)| tee $Log
    restore
    echo
    echo "results in $Log"
    echo "sorted by size:"
    sort -n $Log
}

# Add procfilter plugin.
function add_procfilter(){
    sed -i "/.*procfilter.*/d" "$Plugin_import_inputs"
    sed -i "s|)|$Import_procfilter\n)|" "$Plugin_import_inputs"
    echo "procfilter added"
}


function main(){
    parse_opts "$@"

    # GOPATH is a prerequisite
    if [[ -z "$GOPATH" ]]; then
	echo "This script assumes you have the GOPATH env variable set ($GOPATH/src is were 'go get' will install sources.)"
	exit 1
    fi

    if [[ "$Black" = 1 ]]; then
	disable_plugins_bl "$Plugins_black_list"
    elif [[ "$White" = 1 ]]; then
	disable_plugins_wl "$Plugins_white_list"
    elif [[ "$Size" = 1 ]]; then
	plugin_sizes
	exit 0
    fi

    if [[ $Vsphere = 1 ]]; then
	echo "Using a Vsphere/Vcenter/Vmware white list."
	disable_plugins_wl "$Plugins_vmware_white_list"
	disable_plugin "inputs/procfilter"
    elif [[ $Windows = 1 ]]; then
	echo "Using a Windows white list."
	disable_plugins_wl "$Plugins_windows_white_list"
	disable_plugin "inputs/procfilter"
    else
	# The usual Linux binary wit procfilter.
	add_procfilter
    fi
    exec_size
    echo "Final binary size: ${Ret_size}"
}

main "$@"
