# gathersrv - CoreDNS plugin

## Name

*gathersrv* - gather DNS responses with SRV records from several domains (for example k8s clusters) and hide them behind a single **common/distributed** domain

## Description

This plugin could be helpful for services that are logically distributed over several k8s clusters and use [headless service](https://kubernetes.io/docs/concepts/services-networking/service/#headless-services) to expose themselves.
The aim of this plugin is to provide a method to discover all service instances through a single service domain. The result of querying distributed service domain contains
masqueraded results gathered from multiple clusters.


## Use case


Let's assume there are:
* two k8s clusters - cluster-a, cluster-b
* two zones for above clusters cluster-a.local, cluster-b.local
* possibility to query k8s dns outside cluster
* headless service - demo-service deployed on above clusters in the same namespace (default)

If we ask about

```
dig -t SRV _demo._tcp.demo-service.default.svc.cluster-a.local
```

we will see result as below:

```
;; ANSWER SECTION:
_demo._tcp.demo-service.default.svc.cluster-a.local. 30 IN SRV 0 50 8080 demo-service-0.default.svc.cluster-a.local.
_demo._tcp.demo-service.default.svc.cluster-a.local. 30 IN SRV 0 50 8080 demo-service-1.default.svc.cluster-a.local.

;; ADDITIONAL SECTION:
demo-service-0.default.svc.cluster-a.local. 30 IN A 10.8.1.2
demo-service-1.default.svc.cluster-a.local. 30 IN A 10.8.1.2
```

Respectively for second cluster

```
dig -t SRV _demo._tcp.demo-service.default.svc.cluster-a.local

...

;; ANSWER SECTION:
_demo._tcp.demo-service.default.svc.cluster-b.local. 30 IN SRV 0 50 8080 demo-service-0.default.svc.cluster-b.local.
_demo._tcp.demo-service.default.svc.cluster-b.local. 30 IN SRV 0 50 8080 demo-service-1.default.svc.cluster-b.local.

;; ADDITIONAL SECTION:
demo-service-0.default.svc.cluster-b.local. 30 IN A 10.9.1.2
demo-service-1.default.svc.cluster-b.local. 30 IN A 10.9.1.2
```

Using gathersrv plugin with coredns we can configure it to provide merged information behind single domain - in this case distributed.local



```
dig -t SRV _demo._tcp.demo-service.default.svc.distributed.local

...

;; ANSWER SECTION:
_demo._tcp.demo-service.default.svc.distributed.local. 30 IN SRV 0 50 8080 a-demo-service-0.default.svc.distributed.local.
_demo._tcp.demo-service.default.svc.distributed.local. 30 IN SRV 0 50 8080 a-demo-service-1.default.svc.distributed.local.
_demo._tcp.demo-service.default.svc.distributed.local. 30 IN SRV 0 50 8080 b-demo-service-0.default.svc.distributed.local.
_demo._tcp.demo-service.default.svc.distributed.local. 30 IN SRV 0 50 8080 b-demo-service-1.default.svc.distributed.local.

;; ADDITIONAL SECTION:
a-demo-service-0.default.svc.distributed.local. 30 IN A 10.8.1.2
a-demo-service-1.default.svc.distributed.local. 30 IN A 10.8.1.2
b-demo-service-0.default.svc.distributed.local. 30 IN A 10.9.1.2
b-demo-service-1.default.svc.distributed.local. 30 IN A 10.9.1.2
```


As shown above - the result response not only contains proper ip addresses but also translated hostnames.
This translation adds some prefix which indicates original cluster and replaces cluster domain (.cluster-a.local., .cluster-b.local.) with distributed domain.
In effect service hostnames share their parent domain with service - a-demo-service-0.**default.svc.distributed.local.**.
Thanks to that the result could be consumed by restricted service drivers for example [mongodb+srv](https://docs.mongodb.com/manual/reference/connection-string/#dns-seed-list-connection-format).

It is worth mentioning that the POD's ip addresses will need to be routable outside of cluster-a and cluster-b if you want to connect to them.

## Compilation

This package will always be compiled as part of CoreDNS and not in a standalone way. It will require you to use `go get` or as a dependency on [plugin.cfg](https://github.com/coredns/coredns/blob/master/plugin.cfg).

The [manual](https://coredns.io/manual/toc/#what-is-coredns) will have more information about how to configure and extend the server with external plugins.

A simple way to consume this plugin, is by adding the following on [plugin.cfg](https://github.com/coredns/coredns/blob/master/plugin.cfg), and recompile it as [detailed on coredns.io](https://coredns.io/2017/07/25/compile-time-enabling-or-disabling-plugins/#build-with-compile-time-configuration-file).

~~~
gathersrv:github.com/ziollek/gathersrv
~~~

Put this early in the plugin list, so that **gathersrv** is executed before any of the other plugins.

After this you can compile coredns by:

``` sh
go generate
go build
```

Or you can instead use make:

``` sh
make
```

## Syntax

~~~ txt
gathersrv DISTRIBUTED_DOMAIN {
    CLUSTER_DOMAIN_ONE HOSTNAME_PREFIX_ONE
    ...
    CLUSTER_DOMAIN_N HOSTNAME_PREFIX_N
}
~~~

## Configuration

Below configuration reflects example from use case.
Addresses of dns service for cluster-a and cluster-b are 10.8.0.1, 10.9.0.1 respectively.

```
distributed.local. {
  gathersrv distribiuted.local. {
	cluster-a.local. a-
	cluster-b.local. b-
  }
  forward . 127.0.0.1:5300
}

cluster-a.local.:5300 {
  forward . 10.8.0.1:53
}

cluster-b.local.:5300 {
  forward . 10.9.0.1:53
}
```

## Metrics

| Metric    | Labels                                                 | Description                         |
|-----------|--------------------------------------------------------|-------------------------------------|
| request_count_total    | server, qualified (that will be proxied further), type | Count of requests handled by plugin |
| sub_request_count_total    | server, prefix, type, code                             | Count of sub-requests generated by plugin |