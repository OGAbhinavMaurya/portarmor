<div align="center">

# portarmor

**Linux ephemeral port exhaustion sentry and outbound connection pool shield.**

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Zero Dependencies](https://img.shields.io/badge/Dependencies-Zero%20(Pure%20Go)-green)](#)

<p align="center">
  <a href="#the-problem">The problem</a> •
  <a href="#how-it-works">How it works</a> •
  <a href="#architecture">Architecture</a> •
  <a href="#comparison">Comparison</a> •
  <a href="#installation">Installation</a> •
  <a href="#usage">Usage</a> •
  <a href="#license">License</a>
</p>

</div>

## The problem

Every outbound TCP connection is identified by its 4-tuple: `(src_ip, src_port, dst_ip, dst_port)`. When applications make outbound requests to an external service or API (e.g. payment gateway, third-party webhook, microservice), the destination IP and port are fixed. In Kubernetes clusters using Source Network Address Translation (SNAT via iptables `MASQUERADE`), all pods on a node share the host node IP, leaving the ephemeral source port as the only variable degree of freedom.

The Linux kernel allocates ephemeral source ports from `net.ipv4.ip_local_port_range` (default `32768 60999`, offering 28,232 usable ports). Under RFC 793, once a TCP connection closes, the socket enters the `TIME_WAIT` state and must linger for $2 \times \text{MSL}$ (60 seconds) to ensure in-flight segments do not corrupt subsequent connections:

```
  Microservice Pod               Linux Kernel Socket Allocator             External API Endpoint
         |                                     |                                     |
         |  HTTP request (unpooled)            |                                     |
         |------------------------------------>|  Allocates source port (e.g. 41001) |
         |                                     |------------------------------------>|
         |                                     |  <----------------------------------|
         |  HTTP 200 OK                        |                                     |
         |<------------------------------------|  Socket enters TIME_WAIT (60s)      |
         |                                     |  [Port 41001 locked for 60 seconds] |
         |                                     |                                     |
         |  ... (500 requests / sec burst)     |                                     |
         |                                     |  All 28,232 ports in TIME_WAIT!     |
         |  connect()                          |                                     |
         |------------------------------------>|  __inet_hash_connect: bhash full!   |
         |  ERROR: EADDRNOTAVAIL (errno 99)    |                                     |
         |<------------------------------------|  "cannot assign requested address"  |
```

This creates a rigid mathematical throughput ceiling:

$$\text{Max Unpooled Throughput} = \frac{28,232 \text{ ports}}{60 \text{ seconds}} \approx \mathbf{470 \text{ connections/second}}$$

A burst of unpooled HTTP requests (e.g. Go `http.Client` without shared transport, Python `requests.get()` without `Session`, Node.js without `agentkeepalive`) saturates all available ephemeral ports within 60 seconds. Once exhausted, the kernel fails socket allocation with `EADDRNOTAVAIL` (errno 99: "cannot assign requested address"). Host CPU and memory metrics show idle capacity while all outbound network traffic fails completely.

### Why existing workarounds fail

- **`tcp_tw_recycle`**: Permanently removed from the Linux kernel in version 4.12+ because it dropped connections from clients behind NAT gateways.
- **`tcp_tw_reuse = 1`**: Requires strictly monotonic TCP timestamps (`net.ipv4.tcp_timestamps = 1`) on both client and server. Cloud NAT gateways (AWS NAT Gateway, Azure NAT Gateway) and corporate firewalls routinely strip or rewrite TCP timestamps, causing `tcp_tw_reuse` to fail silently.
- **Widening port range (`1024 65535`)**: Expands capacity to ~64,000 ports, buying only 30 seconds of headroom before collapsing under 1,000 conn/s.
- **Application rewrites**: Tracking down unpooled HTTP clients across polyglot microservice fleets takes weeks of developer time.

## How it works

`portarmor` provides inspection and active connection pooling to prevent ephemeral port starvation without application code changes:

```
  Client Application                portarmor shield                   External Destination
         |                                 |                                    |
         |  HTTP POST /v1/charges          |                                    |
         |  (unpooled, close)              |                                    |
         |-------------------------------->|  1. Intercepts on loopback (8080)  |
         |                                 |  2. Reuses warm pooled connection  |
         |                                 |     to remote host:443             |
         |                                 |----------------------------------->|
         |                                 |  <---------------------------------| [200 OK]
         |  HTTP 200 OK (X-PortArmor-Pooled)                                    |
         |<--------------------------------|                                    |
         |  [Loopback socket recycled]     |  [Remote socket stays established] |
```

1. **`doctor`**: Parses `/proc/net/tcp` and `/proc/sys/net/ipv4/ip_local_port_range`. It groups active and `TIME_WAIT` sockets by `(Destination IP, Destination Port)` endpoint sink, computes real-time saturation percentages, and alerts on port exhaustion risks before `EADDRNOTAVAIL` occurs.
2. **`shield`**: A lightweight loopback reverse proxy running on `127.0.0.1:<port>`. It accepts unpooled outbound requests from local services and forwards them across a persistent keep-alive connection pool (`http.Transport`). Sockets to the external target remain warm and established, entirely bypassing the 60-second `TIME_WAIT` accumulation cycle on the host.

## Architecture

`portarmor` is written in pure Go with zero external dependencies.

```
portarmor/
├── cmd/
│   └── portarmor/
│       ├── main.go             # CLI interface: doctor and shield commands
│       └── main_test.go        # Command line integration tests
├── pkg/
│   ├── procnet/
│   │   ├── parser.go           # /proc/net/tcp hex decoder and state mapper
│   │   ├── range.go            # ip_local_port_range reader
│   │   └── procnet_test.go     # Hex parsing and port capacity tests
│   ├── sentry/
│   │   ├── doctor.go           # 4-tuple aggregator, saturation %, and sink rankings
│   │   └── doctor_test.go      # Saturation and threshold evaluation tests
│   └── shield/
│       ├── proxy.go            # Outbound connection pool proxy with keepalive reuse
│       └── proxy_test.go       # End-to-end proxy forwarding tests
├── go.mod
└── README.md
```

## Installation

Building from source:

```bash
git clone https://github.com/OGAbhinavMaurya/portarmor.git
cd portarmor
go build -o portarmor ./cmd/portarmor
sudo install -m 0755 portarmor /usr/local/bin/
```

## Usage

### 1. Doctor (port saturation sentry)

Inspect active TCP sockets and identify destination sinks consuming ephemeral ports:

```bash
# Run doctor inspection on local host
portarmor doctor

# Set warning threshold to 50% saturation and output JSON
portarmor doctor --threshold 50.0 --json
```

Example doctor output during an outbound traffic spike:
```text
================================================================================
                            PORTARMOR DOCTOR REPORT                             
================================================================================
 Ephemeral Port Range:   32768 - 60999 (Capacity: 28232 ports)
 Total Active Sockets:   23410
 TIME_WAIT Sockets:      22850
 ESTABLISHED Sockets:    560
 Overall Saturation:     82.92%
 Status:                 [CRITICAL]
 Summary:                Critical ephemeral port exhaustion risk. Destination 52.84.12.98:443 consumes 80.9% of local port capacity (22850 sockets). EADDRNOTAVAIL (errno 99) failures imminent.
 Recommendation:         Deploy 'portarmor shield' to pool connections to 52.84.12.98:443 and eliminate TIME_WAIT accumulation.
--------------------------------------------------------------------------------
 Top Destination Sinks:
   [1] 52.84.12.98:443          Total: 22850 (TIME_WAIT: 22850, ESTAB: 0    ) Saturation: 80.94%
   [2] 10.0.1.50:5432           Total: 500   (TIME_WAIT: 0    , ESTAB: 500  ) Saturation:  1.77%
   [3] 10.0.2.10:6379           Total: 60    (TIME_WAIT: 0    , ESTAB: 60   ) Saturation:  0.21%
================================================================================
```

### 2. Shield (outbound connection pool proxy)

Wrap an unpooled outbound endpoint with a persistent loopback connection pool:

```bash
# Listen on 8080 and forward to payment gateway with a pool of 100 connections
portarmor shield --listen 127.0.0.1:8080 --target https://api.stripe.com --pool-size 100
```

Point your local application to `http://127.0.0.1:8080` instead of the direct external URL. Unpooled client connections are terminated locally while outbound requests are multiplexed over warm, persistent sockets, completely eliminating outbound `TIME_WAIT` socket buildup.

## Comparison

| Feature | `portarmor` | `tcp_tw_reuse = 1` | `tcp_tw_recycle` | Widening Port Range |
| :--- | :--- | :--- | :--- | :--- |
| **Bypasses 470 conn/s ceiling** | Yes (pools connections) | Partial | N/A (removed) | No (delays by ~30s) |
| **Compatible with Cloud NAT** | Yes | No (requires monotonic timestamps) | No (drops packets) | Yes |
| **Destination Sink Attribution** | Yes (per-IP:port stats) | No (`ss -s` only gives total) | No | No |
| **Zero Code Changes Required** | Yes (local proxy) | Yes (sysctl) | N/A | Yes (sysctl) |
| **Kernel Version Requirement** | Any Linux (userspace) | Linux 3.x+ | Removed in 4.12 | Any Linux |
| **Runtime Footprint** | Single static Go binary (~8MB) | Kernel flag | Removed | Kernel flag |

## License

MIT
