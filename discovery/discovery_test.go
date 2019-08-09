package discovery

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"
)

type mockDiscoverer struct {
	result []Service
}

func (md mockDiscoverer) Discovery(ctx context.Context, maxAge time.Duration) (services []Service, err error) {
	return md.result, nil
}

// Test dynamic Discovery with single service present
func TestDiscoverySingle(t *testing.T) {

	cases := []struct {
		dynamicResult   Service
		previousService Service
		want            Service
	}{
		{
			previousService: Service{},
			dynamicResult: Service{
				Name:            MemcachedService,
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "127.0.0.1:11211"}},
				IPAddress:       "127.0.0.1",
				hasNetstatInfo:  true,
			},
			want: Service{
				Name:            MemcachedService,
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "127.0.0.1:11211"}},
				IPAddress:       "127.0.0.1",
				hasNetstatInfo:  true,
			},
		},
		{
			previousService: Service{
				Name:            MemcachedService,
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "127.0.0.1:11211"}},
				IPAddress:       "127.0.0.1",
				hasNetstatInfo:  true,
			},
			dynamicResult: Service{
				Name:            MemcachedService,
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "127.0.0.1:11211"}},
				IPAddress:       "127.0.0.1",
				hasNetstatInfo:  true,
			},
			want: Service{
				Name:            MemcachedService,
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "127.0.0.1:11211"}},
				IPAddress:       "127.0.0.1",
				hasNetstatInfo:  true,
			},
		},
		{
			previousService: Service{
				Name:            MemcachedService,
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "10.0.0.5:11211"}},
				IPAddress:       "10.0.0.5",
				hasNetstatInfo:  true,
			},
			dynamicResult: Service{
				Name:            MemcachedService,
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "127.0.0.1:11211"}},
				IPAddress:       "",
				hasNetstatInfo:  false,
			},
			want: Service{
				Name:            MemcachedService,
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "10.0.0.5:11211"}},
				IPAddress:       "10.0.0.5",
				hasNetstatInfo:  true,
			},
		},
		{
			previousService: Service{
				Name:            MemcachedService,
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "10.0.0.5:11211"}},
				IPAddress:       "10.0.0.5",
				hasNetstatInfo:  true,
			},
			dynamicResult: Service{
				Name:            MemcachedService,
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "127.0.0.1:11211"}},
				IPAddress:       "127.0.0.1",
				hasNetstatInfo:  true,
			},
			want: Service{
				Name:            MemcachedService,
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "127.0.0.1:11211"}},
				IPAddress:       "127.0.0.1",
				hasNetstatInfo:  true,
			},
		},
	}

	ctx := context.Background()
	for i, c := range cases {
		var previousService []Service
		if c.previousService.Name != "" {
			previousService = append(previousService, c.previousService)
		}
		disc := New(mockDiscoverer{result: []Service{c.dynamicResult}}, nil, nil, previousService, nil)

		srv, err := disc.Discovery(ctx, 0)
		if err != nil {
			t.Error(err)
		}
		if len(srv) != 1 {
			t.Errorf("Case #%d: len(srv) == %v, want 1", i, len(srv))
		}
		if srv[0].Name != c.want.Name {
			t.Errorf("Case #%d: Name == %#v, want %#v", i, srv[0].Name, c.want.Name)
		}
		if srv[0].ContainerID != c.want.ContainerID {
			t.Errorf("Case #%d: ContainerID == %#v, want %#v", i, srv[0].ContainerID, c.want.ContainerID)
		}
		if srv[0].IPAddress != c.want.IPAddress {
			t.Errorf("Case #%d: IPAddress == %#v, want %#v", i, srv[0].IPAddress, c.want.IPAddress)
		}
		if !reflect.DeepEqual(srv[0].ListenAddresses, c.want.ListenAddresses) {
			t.Errorf("Case #%d: ListenAddresses == %v, want %v", i, srv[0].ListenAddresses, c.want.ListenAddresses)
		}
		if srv[0].hasNetstatInfo != c.want.hasNetstatInfo {
			t.Errorf("Case #%d: hasNetstatInfo == %#v, want %#v", i, srv[0].hasNetstatInfo, c.want.hasNetstatInfo)
		}
	}
}
