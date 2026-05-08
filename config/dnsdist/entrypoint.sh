#!/bin/sh
KRESD_IP=$(getent hosts kresd | awk '{print $1}')
if [ -z "$KRESD_IP" ]; then
    echo "ERROR: Cannot resolve kresd hostname" >&2
    exit 1
fi

cat /etc/dnsdist/dnsdist.conf.tpl | sed "s/__KRESD_IP__/${KRESD_IP}/g" > /tmp/dnsdist.conf
echo "Resolved kresd to ${KRESD_IP}"
exec dnsdist --config /tmp/dnsdist.conf --supervised
