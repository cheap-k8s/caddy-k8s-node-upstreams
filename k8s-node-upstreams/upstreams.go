package upstreams

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(K8sNodeUpstreams{})
}

type K8sNodeUpstreams struct {
	NodeNamePrefix string `json:"node_name_prefix"`

	logger *zap.Logger
}

// CaddyModule returns the Caddy module information.
func (K8sNodeUpstreams) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.upstreams.k8s_node",
		New: func() caddy.Module { return new(K8sNodeUpstreams) },
	}
}

// Provision sets up the module.
func (u *K8sNodeUpstreams) Provision(ctx caddy.Context) error {
	u.logger = ctx.Logger(u)
	lookup = k8sNodeLookup{
		k8sNodeUpstream: u,
	}

	return nil
}

func (u K8sNodeUpstreams) GetUpstreams(r *http.Request) ([]*reverseproxy.Upstream, error) {
	done := make(chan bool)
	go lookup.updateUpstreams(done)
	<-done

	return lookup.upstreams, nil
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler. Syntax:
//
//	dynamic k8s_node {
//		node_name_prefix <node_name_prefix>
//	}
func (u *K8sNodeUpstreams) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		args := d.RemainingArgs()

		if len(args) > 0 {
			return d.ArgErr()
		}

		for d.NextBlock(0) {
			switch d.Val() {
			case "node_name_prefix":
				if !d.NextArg() {
					return d.ArgErr()
				}
				if u.NodeNamePrefix != "" {
					return d.Errf("k8s_node node name prefix has already been specified")
				}
				u.NodeNamePrefix = d.Val()
			default:
				return d.Errf("unrecognized k8s_node option '%s'", d.Val())
			}
		}
	}
	return nil
}

func (u K8sNodeUpstreams) String() string {
	return "k8s_node_upstream"
}

type k8sNodeLookup struct {
	k8sNodeUpstream *K8sNodeUpstreams
	ipTimestamps    map[string]time.Time
	updateing       bool
	freshness       time.Time
	upstreams       []*reverseproxy.Upstream
}

func (l *k8sNodeLookup) isFresh() bool {
	return time.Since(l.freshness) < 1*time.Minute
}

func (l *k8sNodeLookup) updateUpstreams(done chan bool) {
	if l.isFresh() {
		done <- true
		return
	}

	if l.updateing {
		done <- true
		return
	}

	lookupMu.Lock()
	defer lookupMu.Unlock()
	l.updateing = true
	for {
		nodeIps, err := l.listNodeIps()
		l.k8sNodeUpstream.logger.Info("node ips: " + strings.Join(nodeIps, ","))
		if err == nil {
			l.updateIpTimestamps(nodeIps)
			activeIps := l.getActiveIps(nodeIps)
			l.k8sNodeUpstream.logger.Info("activeIps: " + strings.Join(activeIps, ","))
			upstreams := make([]*reverseproxy.Upstream, len(activeIps))
			for i, ip := range activeIps {
				upstreams[i] = &reverseproxy.Upstream{
					Dial: net.JoinHostPort(ip, "30080"),
				}
			}
			l.upstreams = upstreams
			l.freshness = time.Now()
			break
		}
		l.k8sNodeUpstream.logger.Error("listInstanceIps failed: " + err.Error())
		time.Sleep(1 * time.Minute)
	}
	l.updateing = false
	done <- true
}

func (l *k8sNodeLookup) getActiveIps(nodeIps []string) []string {
	now := time.Now()
	activeIps := make([]string, 0)
	for _, ip := range nodeIps {
		if t, ok := l.ipTimestamps[ip]; ok {
			if now.Sub(t) >= 5*time.Minute {
				activeIps = append(activeIps, ip)
			}
		}
	}
	return activeIps
}

func (l *k8sNodeLookup) updateIpTimestamps(nodeIps []string) {
	initialized := len(l.ipTimestamps) > 0

	var s time.Time
	if initialized {
		s = time.Now()
	} else {
		l.ipTimestamps = make(map[string]time.Time)
		s = time.Now().Add(-5 * time.Minute)
	}

	for _, ip := range nodeIps {
		if _, ok := l.ipTimestamps[ip]; !ok {
			l.ipTimestamps[ip] = s
		}
	}

	for ip, t := range l.ipTimestamps {
		l.k8sNodeUpstream.logger.Info("ip: " + ip + ", stamp: " + t.String())
	}
}

func (l *k8sNodeLookup) listNodeIps() ([]string, error) {
	ctx := context.Background()
	credentials, err := google.FindDefaultCredentials(ctx)
	if err != nil {
		return nil, err
	}
	client, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	filter := fmt.Sprintf("name = %s*", l.k8sNodeUpstream.NodeNamePrefix)
	req := &computepb.AggregatedListInstancesRequest{
		Project: credentials.ProjectID,
		Filter:  &filter,
	}
	it := client.AggregatedList(ctx, req)
	var ips []string
	for {
		resp, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(resp.Value.Instances) > 0 {
			for _, i := range resp.Value.Instances {
				if *i.Status == "RUNNING" {
					ips = append(ips, *i.NetworkInterfaces[0].NetworkIP)
				}
			}
		}
	}
	return ips, nil
}

var (
	lookup   k8sNodeLookup
	lookupMu sync.RWMutex
)

// Interface guards
var (
	_ caddy.Provisioner           = (*K8sNodeUpstreams)(nil)
	_ reverseproxy.UpstreamSource = (*K8sNodeUpstreams)(nil)
	_ caddyfile.Unmarshaler       = (*K8sNodeUpstreams)(nil)
)
