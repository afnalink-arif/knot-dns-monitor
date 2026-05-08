-- dnsdist.conf — Packet cache proxy for kresd
-- Purpose: serve cached DNS responses during kresd restart (RPZ load ~38s)
-- Port 53 public -> dnsdist -> kresd:53 (Docker internal)
-- Template: __KRESD_IP__ replaced at runtime by entrypoint.sh

setLocal("0.0.0.0:53")
addLocal("[::]:53")

setACL({"0.0.0.0/0", "::/0"})

newServer("__KRESD_IP__")

setServerPolicy(firstAvailable)

pc = newPacketCache(500000, {
    maxTTL=300,
    minTTL=5,
    temporaryFailureTTL=30,
    staleTTL=3600,
    dontAge=false,
    numberOfShards=16
})
getPool(""):setCache(pc)

setVerboseHealthChecks(false)
