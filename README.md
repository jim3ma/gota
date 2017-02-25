# [WIP] Gota - a TCP Traffic Aggregator Written in Golang

Gota is a userspace multipath tcp solution.

Gota Active End(aka client) receives tcp traffic and forwards to Gota Passive End(aka server) use multipath tcp connections.

## Usage Scenario

Sometimes, our network administrator enables rate-limiting for every tcp connection, the speed of our tcp connections can't reach the top. But if we create multi tcp connections, the traffic of all connections can be aggregated to reach the top speed.

## Quick Start

We can use Gota as a library, or daemon.

### Use Gota as Daemon

#### Get Gota binary

* Build from source

```shell
go get github.com/jim3ma/gota/gota
```

Or

* [TBD] Download from [Release](https://github.com/jim3ma/gota/releases) page according to you os and arch

#### Update Server Configuration

> PS: the config files already exist in `examples`, you can make a reference.

* Update and save as config.server.yml

```yml
# default for server, did not change it
mode: server

# log level: info debug warn error fatal panic
log: debug

# tunnel authenticate credential
auth:
  username: gota
  password: gota

# remote address with port for forwarding traffic
# suppose you want to speed up you 8080 port
remote: 127.0.0.1:8080

# tunnel listen address with port
tunnel:
  - listen: 127.0.0.1:12333
  - listen: 127.0.0.1:12336
```

* Launch Server

```shell
gota server --config config.server.yml
```

#### Update Client Configuration

* Update and save as config.client.yml

```yml
# default for server, did not change it
mode: client

# log level: info debug warn error fatal panic
log: debug

# tunnel authenticate credential
auth:
  username: gota
  password: gota

# local listen address with port
listen: 127.0.0.1:12363

# Gota server addresses with port
tunnel:
  - remote: 127.0.0.1:12333 # connect server directly
  - remote: 127.0.0.1:12336
    # connect server using a proxy, currently Gota support http/https/socks5 proxy
    proxy: http://gota:gota@127.0.0.1:3128
```

* Launch Client

```shell
gota client --config config.client.yml
```

#### Try yourself

```shell
telnet 127.0.0.1:12363
# or
curl 127.0.0.1:12363
```

### Use Gota as a library

TBD

## Architecture

![Gota Architecture](./architecture.png)

## Contributing

Contributions are welcome.

## Copyright / License

Copyright 2013-2017 Jim Ma

This software is licensed under the terms of the Apache License Version 2. See the [LICENSE](./LICENSE) file.

