package upstreams

import (
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
)

func Test_k8sNodeLookup_listInstanceIps(t *testing.T) {
	type fields struct {
		k8sNodeUpstream *K8sNodeUpstreams
		updateing       bool
		freshness       time.Time
		upstreams       []*reverseproxy.Upstream
	}
	tests := []struct {
		name    string
		fields  fields
		want    []string
		wantErr bool
	}{
		{
			name: "Returns GKE Node intances's internal IPs",
			fields: fields{
				k8sNodeUpstream: &K8sNodeUpstreams{
					NodeNamePrefix: "gke-cluster-c5fe837",
				},
			},
			want: []string{"10.128.0.3", "10.128.0.5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &k8sNodeLookup{
				k8sNodeUpstream: tt.fields.k8sNodeUpstream,
				updateing:       tt.fields.updateing,
				freshness:       tt.fields.freshness,
				upstreams:       tt.fields.upstreams,
			}
			got, err := l.listInstanceIps()
			if (err != nil) != tt.wantErr {
				t.Errorf("k8sNodeLookup.listInstanceIps() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("k8sNodeLookup.listInstanceIps() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_k8sNodeLookup_updateUpstreams(t *testing.T) {
	type fields struct {
		k8sNodeUpstream *K8sNodeUpstreams
		updateing       bool
		freshness       time.Time
		upstreams       []*reverseproxy.Upstream
	}
	tests := []struct {
		name   string
		fields fields
		want   []*reverseproxy.Upstream
	}{
		{
			name: "Fetchs IPs & Updates upstreams",
			fields: fields{
				k8sNodeUpstream: &K8sNodeUpstreams{
					NodeNamePrefix: "",
				},
				updateing: false,
			},
			want: []*reverseproxy.Upstream{
				{
					Dial: net.JoinHostPort("10.128.0.3", "32080"),
				},
				{
					Dial: net.JoinHostPort("10.128.0.5", "32080"),
				},
			},
		},
		{
			name: "Returns fresh exist upstreams",
			fields: fields{
				k8sNodeUpstream: &K8sNodeUpstreams{
					NodeNamePrefix: "",
				},
				updateing: false,
				freshness: time.Now(),
				upstreams: []*reverseproxy.Upstream{
					{
						Dial: net.JoinHostPort("10.0.0.1", "32080"),
					},
				},
			},
			want: []*reverseproxy.Upstream{
				{
					Dial: net.JoinHostPort("10.0.0.1", "32080"),
				},
			},
		},
		{
			name: "Returns non-fresh exist upstreams since upstreams are updating",
			fields: fields{
				k8sNodeUpstream: &K8sNodeUpstreams{
					NodeNamePrefix: "",
				},
				updateing: true,
				freshness: time.Now().Add(-2 * time.Minute),
				upstreams: []*reverseproxy.Upstream{
					{
						Dial: net.JoinHostPort("10.0.0.1", "32080"),
					},
				},
			},
			want: []*reverseproxy.Upstream{
				{
					Dial: net.JoinHostPort("10.0.0.1", "32080"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &k8sNodeLookup{
				k8sNodeUpstream: tt.fields.k8sNodeUpstream,
				updateing:       tt.fields.updateing,
				freshness:       tt.fields.freshness,
				upstreams:       tt.fields.upstreams,
			}
			done := make(chan bool)
			go l.updateUpstreams(done)
			<-done
			if !reflect.DeepEqual(l.upstreams, tt.want) {
				t.Errorf("k8sNodeLookup.updateUpstreams() = %v, want %v", l.upstreams, tt.want)
			}
		})
	}
}
