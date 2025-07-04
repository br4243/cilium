// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package loadbalancer

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.yaml.in/yaml/v3"

	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
)

func TestL4Addr_Equals(t *testing.T) {
	type args struct {
		o *L4Addr
	}
	tests := []struct {
		name   string
		fields *L4Addr
		args   args
		want   bool
	}{
		{
			name: "both equal",
			fields: &L4Addr{
				Protocol: NONE,
				Port:     1,
			},
			args: args{
				o: &L4Addr{
					Protocol: NONE,
					Port:     1,
				},
			},
			want: true,
		},
		{
			name: "both different",
			fields: &L4Addr{
				Protocol: NONE,
				Port:     0,
			},
			args: args{
				o: &L4Addr{
					Protocol: NONE,
					Port:     1,
				},
			},
			want: false,
		},
		{
			name: "both nil",
			args: args{},
			want: true,
		},
		{
			name: "other nil",
			fields: &L4Addr{
				Protocol: NONE,
				Port:     1,
			},
			args: args{},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := tt.fields
			if got := l.DeepEqual(tt.args.o); got != tt.want {
				t.Errorf("L4Addr.DeepEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestL3n4Addr_Bytes(t *testing.T) {
	v4 := cmtypes.MustParseAddrCluster("1.1.1.1")
	v4c3 := cmtypes.MustParseAddrCluster("1.1.1.1@3")
	v6 := cmtypes.MustParseAddrCluster("2001::1")
	tests := []struct {
		addr     L3n4Addr
		expected []byte
	}{
		{
			addr: L3n4Addr{
				L4Addr:      L4Addr{Protocol: NONE, Port: 0xabcd},
				AddrCluster: v4,
				Scope:       ScopeExternal,
			},
			expected: []byte{
				0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 255, 255, 1, 1, 1, 1, // IP
				0, 0, 0, 0, // Cluster 0
				0xab, 0xcd, // Port
				'?', // L4Type
				0,   // Scope
			},
		},
		{
			addr: L3n4Addr{
				L4Addr:      L4Addr{Protocol: TCP, Port: 0xabcd},
				AddrCluster: v4c3,
				Scope:       ScopeInternal,
			},
			expected: []byte{
				0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 255, 255, 1, 1, 1, 1, // IP
				0, 0, 0, 3, // Cluster 3
				0xab, 0xcd, // Port
				'T', // L4Type
				1,   // Scope
			},
		},
		{
			addr: L3n4Addr{
				L4Addr:      L4Addr{Protocol: UDP, Port: 0xaabb},
				AddrCluster: v6,
				Scope:       ScopeExternal,
			},
			expected: []byte{
				32, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, // IP
				0, 0, 0, 0, // Cluster 0
				0xaa, 0xbb, // Port
				'U', // L4Type
				0,   // Scope
			},
		},
	}

	for _, test := range tests {
		if !bytes.Equal(test.addr.Bytes(), test.expected) {
			t.Errorf("L3n4Addr.Bytes() = %v, want %v", test.addr.Bytes(), test.expected)
		}
	}
}

func TestL3n4AddrYAML(t *testing.T) {
	tests := []string{
		"0.0.0.0:0/TCP",
		"1.1.1.1:1/UDP",
		"1.1.1.1:65535/UDP",
		"[2001::1]:80/TCP",
		"[2001::1]:80/SCTP",
	}
	for _, test := range tests {
		var l L3n4Addr
		if assert.NoError(t, l.ParseFromString(test), "parse %q", test) {
			out, err := yaml.Marshal(l)
			if assert.NoError(t, err, "Marshal %+v", l) {
				assert.Equal(t, strings.Trim(string(out), "\n'"), test)
				var l2 L3n4Addr
				assert.NoError(t, yaml.Unmarshal(out, &l2))
				assert.True(t, l.DeepEqual(&l2))
			}
		}
	}
}

func TestL3n4AddrID_Equals(t *testing.T) {
	type args struct {
		o *L3n4AddrID
	}
	tests := []struct {
		name   string
		fields *L3n4AddrID
		args   args
		want   bool
	}{
		{
			name: "both equal",
			fields: &L3n4AddrID{
				L3n4Addr: L3n4Addr{
					L4Addr: L4Addr{
						Protocol: NONE,
						Port:     1,
					},
					AddrCluster: cmtypes.MustParseAddrCluster("1.1.1.1"),
				},
				ID: 1,
			},
			args: args{
				o: &L3n4AddrID{
					L3n4Addr: L3n4Addr{
						L4Addr: L4Addr{
							Protocol: NONE,
							Port:     1,
						},
						AddrCluster: cmtypes.MustParseAddrCluster("1.1.1.1"),
					},
					ID: 1,
				},
			},
			want: true,
		},
		{
			name: "IDs different",
			fields: &L3n4AddrID{
				L3n4Addr: L3n4Addr{
					L4Addr: L4Addr{
						Protocol: NONE,
						Port:     1,
					},
					AddrCluster: cmtypes.MustParseAddrCluster("1.1.1.1"),
				},
				ID: 1,
			},
			args: args{
				o: &L3n4AddrID{
					L3n4Addr: L3n4Addr{
						L4Addr: L4Addr{
							Protocol: NONE,
							Port:     1,
						},
						AddrCluster: cmtypes.MustParseAddrCluster("1.1.1.1"),
					},
					ID: 2,
				},
			},
			want: false,
		},
		{
			name: "IPs different",
			fields: &L3n4AddrID{
				L3n4Addr: L3n4Addr{
					L4Addr: L4Addr{
						Protocol: NONE,
						Port:     1,
					},
					AddrCluster: cmtypes.MustParseAddrCluster("2.2.2.2"),
				},
				ID: 1,
			},
			args: args{
				o: &L3n4AddrID{
					L3n4Addr: L3n4Addr{
						L4Addr: L4Addr{
							Protocol: NONE,
							Port:     1,
						},
						AddrCluster: cmtypes.MustParseAddrCluster("1.1.1.1"),
					},
					ID: 1,
				},
			},
			want: false,
		},
		{
			name: "Ports different",
			fields: &L3n4AddrID{
				L3n4Addr: L3n4Addr{
					L4Addr: L4Addr{
						Protocol: NONE,
						Port:     2,
					},
					AddrCluster: cmtypes.MustParseAddrCluster("1.1.1.1"),
				},
				ID: 1,
			},
			args: args{
				o: &L3n4AddrID{
					L3n4Addr: L3n4Addr{
						L4Addr: L4Addr{
							Protocol: NONE,
							Port:     1,
						},
						AddrCluster: cmtypes.MustParseAddrCluster("1.1.1.1"),
					},
					ID: 1,
				},
			},
			want: false,
		},
		{
			name: "both nil",
			args: args{},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.fields
			if got := f.DeepEqual(tt.args.o); got != tt.want {
				t.Errorf("L3n4AddrID.Equals() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestL3n4AddrID_Strings(t *testing.T) {
	tests := []struct {
		name               string
		fields             *L3n4AddrID
		string             string
		stringWithProtocol string
	}{
		{
			name: "IPv4 no protocol",
			fields: &L3n4AddrID{
				L3n4Addr: L3n4Addr{
					L4Addr: L4Addr{
						Protocol: NONE,
						Port:     9876,
					},
					AddrCluster: cmtypes.MustParseAddrCluster("1.1.1.1"),
				},
				ID: 1,
			},
			string:             "1.1.1.1:9876/NONE",
			stringWithProtocol: "1.1.1.1:9876/NONE",
		},
		{
			name: "IPv4 TCP",
			fields: &L3n4AddrID{
				L3n4Addr: L3n4Addr{
					L4Addr: L4Addr{
						Protocol: TCP,
						Port:     9876,
					},
					AddrCluster: cmtypes.MustParseAddrCluster("2.2.2.2"),
					Scope:       ScopeExternal,
				},
				ID: 1,
			},
			string:             "2.2.2.2:9876/TCP",
			stringWithProtocol: "2.2.2.2:9876/TCP",
		},
		{
			name: "IPv4 UDP",
			fields: &L3n4AddrID{
				L3n4Addr: L3n4Addr{
					L4Addr: L4Addr{
						Protocol: UDP,
						Port:     9876,
					},
					AddrCluster: cmtypes.MustParseAddrCluster("3.3.3.3"),
					Scope:       ScopeInternal,
				},
				ID: 1,
			},
			string:             "3.3.3.3:9876/UDP/i",
			stringWithProtocol: "3.3.3.3:9876/UDP/i",
		},
		{
			name: "IPv4 SCTP",
			fields: &L3n4AddrID{
				L3n4Addr: L3n4Addr{
					L4Addr: L4Addr{
						Protocol: SCTP,
						Port:     9876,
					},
					AddrCluster: cmtypes.MustParseAddrCluster("4.4.4.4"),
				},
				ID: 1,
			},
			string:             "4.4.4.4:9876/SCTP",
			stringWithProtocol: "4.4.4.4:9876/SCTP",
		},
		{
			name: "IPv6 no protocol",
			fields: &L3n4AddrID{
				L3n4Addr: L3n4Addr{
					L4Addr: L4Addr{
						Protocol: NONE,
						Port:     9876,
					},
					AddrCluster: cmtypes.MustParseAddrCluster("1020:3040:5060:7080:90a0:b0c0:d0e0:f000"),
				},
				ID: 1,
			},
			string:             "[1020:3040:5060:7080:90a0:b0c0:d0e0:f000]:9876/NONE",
			stringWithProtocol: "[1020:3040:5060:7080:90a0:b0c0:d0e0:f000]:9876/NONE",
		},
		{
			name: "IPv6 TCP",
			fields: &L3n4AddrID{
				L3n4Addr: L3n4Addr{
					L4Addr: L4Addr{
						Protocol: TCP,
						Port:     9876,
					},
					AddrCluster: cmtypes.MustParseAddrCluster("1020:3040:5060:7080:90a0:b0c0:d0e0:f000"),
					Scope:       ScopeExternal,
				},
				ID: 1,
			},
			string:             "[1020:3040:5060:7080:90a0:b0c0:d0e0:f000]:9876/TCP",
			stringWithProtocol: "[1020:3040:5060:7080:90a0:b0c0:d0e0:f000]:9876/TCP",
		},
		{
			name: "IPv6 UDP",
			fields: &L3n4AddrID{
				L3n4Addr: L3n4Addr{
					L4Addr: L4Addr{
						Protocol: UDP,
						Port:     9876,
					},
					AddrCluster: cmtypes.MustParseAddrCluster("1020:3040:5060:7080:90a0:b0c0:d0e0:f000"),
					Scope:       ScopeInternal,
				},
				ID: 1,
			},
			string:             "[1020:3040:5060:7080:90a0:b0c0:d0e0:f000]:9876/UDP/i",
			stringWithProtocol: "[1020:3040:5060:7080:90a0:b0c0:d0e0:f000]:9876/UDP/i",
		},
		{
			name: "IPv6 SCTP",
			fields: &L3n4AddrID{
				L3n4Addr: L3n4Addr{
					L4Addr: L4Addr{
						Protocol: SCTP,
						Port:     9876,
					},
					AddrCluster: cmtypes.MustParseAddrCluster("1020:3040:5060:7080:90a0:b0c0:d0e0:f000"),
				},
				ID: 1,
			},
			string:             "[1020:3040:5060:7080:90a0:b0c0:d0e0:f000]:9876/SCTP",
			stringWithProtocol: "[1020:3040:5060:7080:90a0:b0c0:d0e0:f000]:9876/SCTP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.fields
			string := f.String()
			if string != tt.string {
				t.Errorf("L3n4AddrID.String() = %s, want %s", string, tt.string)
			}
			strWithProtocol := f.StringWithProtocol()
			if strWithProtocol != tt.stringWithProtocol {
				t.Errorf("L3n4AddrID.StringWithProtocol() = %s, want %s", strWithProtocol, tt.stringWithProtocol)
			}
		})
	}
}

func TestNewSvcFlag(t *testing.T) {
	type args struct {
		svcType     SVCType
		svcExtLocal bool
		svcIntLocal bool
		svcRoutable bool
		svcL7LB     bool
	}
	tests := []struct {
		name string
		args args
		want ServiceFlags
	}{
		{
			args: args{
				svcType:     SVCTypeClusterIP,
				svcExtLocal: false,
				svcIntLocal: false,
				svcRoutable: true,
			},
			want: serviceFlagNone | serviceFlagRoutable,
		},
		{
			args: args{
				svcType:     SVCTypeNodePort,
				svcExtLocal: false,
				svcIntLocal: false,
				svcRoutable: true,
			},
			want: serviceFlagNodePort | serviceFlagRoutable,
		},
		{
			args: args{
				svcType:     SVCTypeExternalIPs,
				svcExtLocal: false,
				svcIntLocal: false,
				svcRoutable: true,
			},
			want: serviceFlagExternalIPs | serviceFlagRoutable,
		},
		{
			// Impossible combination, ClusterIP can't have externalTrafficPolicy=Local.
			args: args{
				svcType:     SVCTypeClusterIP,
				svcExtLocal: true,
				svcIntLocal: false,
				svcRoutable: true,
			},
			want: serviceFlagNone | serviceFlagExtLocalScope | serviceFlagRoutable,
		},
		{
			args: args{
				svcType:     SVCTypeNodePort,
				svcExtLocal: true,
				svcIntLocal: false,
				svcRoutable: true,
			},
			want: serviceFlagNodePort | serviceFlagExtLocalScope | serviceFlagTwoScopes | serviceFlagRoutable,
		},
		{
			args: args{
				svcType:     SVCTypeExternalIPs,
				svcExtLocal: true,
				svcIntLocal: false,
				svcRoutable: true,
			},
			want: serviceFlagExternalIPs | serviceFlagExtLocalScope | serviceFlagTwoScopes | serviceFlagRoutable,
		},
		{
			args: args{
				svcType:     SVCTypeClusterIP,
				svcExtLocal: false,
				svcIntLocal: true,
				svcRoutable: true,
			},
			want: serviceFlagNone | serviceFlagIntLocalScope | serviceFlagRoutable,
		},
		{
			args: args{
				svcType:     SVCTypeNodePort,
				svcExtLocal: false,
				svcIntLocal: true,
				svcRoutable: true,
			},
			want: serviceFlagNodePort | serviceFlagIntLocalScope | serviceFlagTwoScopes | serviceFlagRoutable,
		},
		{
			args: args{
				svcType:     SVCTypeExternalIPs,
				svcExtLocal: false,
				svcIntLocal: true,
				svcRoutable: true,
			},
			want: serviceFlagExternalIPs | serviceFlagIntLocalScope | serviceFlagTwoScopes | serviceFlagRoutable,
		},
		{
			// Impossible combination, ClusterIP can't have externalTrafficPolicy=Local.
			args: args{
				svcType:     SVCTypeClusterIP,
				svcExtLocal: true,
				svcIntLocal: true,
				svcRoutable: true,
			},
			want: serviceFlagNone | serviceFlagExtLocalScope | serviceFlagIntLocalScope | serviceFlagRoutable,
		},
		{
			args: args{
				svcType:     SVCTypeNodePort,
				svcExtLocal: true,
				svcIntLocal: true,
				svcRoutable: true,
			},
			want: serviceFlagNodePort | serviceFlagExtLocalScope | serviceFlagIntLocalScope | serviceFlagRoutable,
		},
		{
			args: args{
				svcType:     SVCTypeExternalIPs,
				svcExtLocal: true,
				svcIntLocal: true,
				svcRoutable: true,
			},
			want: serviceFlagExternalIPs | serviceFlagExtLocalScope | serviceFlagIntLocalScope | serviceFlagRoutable,
		},
		{
			args: args{
				svcType:     SVCTypeExternalIPs,
				svcExtLocal: true,
				svcIntLocal: false,
				svcRoutable: false,
			},
			want: serviceFlagExternalIPs | serviceFlagExtLocalScope | serviceFlagTwoScopes,
		},
		{
			args: args{
				svcType:     SVCTypeExternalIPs,
				svcExtLocal: false,
				svcIntLocal: true,
				svcRoutable: false,
			},
			want: serviceFlagExternalIPs | serviceFlagIntLocalScope | serviceFlagTwoScopes,
		},
		{
			args: args{
				svcType:     SVCTypeExternalIPs,
				svcExtLocal: true,
				svcIntLocal: true,
				svcRoutable: false,
			},
			want: serviceFlagExternalIPs | serviceFlagExtLocalScope | serviceFlagIntLocalScope,
		},
		{
			args: args{
				svcType:     SVCTypeLocalRedirect,
				svcExtLocal: false,
				svcIntLocal: false,
				svcRoutable: true,
			},
			want: serviceFlagLocalRedirect | serviceFlagRoutable,
		},
		{
			args: args{
				svcType: SVCTypeClusterIP,
				svcL7LB: true,
			},
			want: serviceFlagL7LoadBalancer,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &SvcFlagParam{
				SvcExtLocal:     tt.args.svcExtLocal,
				SvcIntLocal:     tt.args.svcIntLocal,
				SessionAffinity: false,
				IsRoutable:      tt.args.svcRoutable,
				SvcType:         tt.args.svcType,
				L7LoadBalancer:  tt.args.svcL7LB,
			}
			if got := NewSvcFlag(p); got != tt.want {
				t.Errorf("NewSvcFlag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServiceFlags_String(t *testing.T) {
	tests := []struct {
		name string
		s    ServiceFlags
		want string
	}{
		{
			name: "Test-1",
			s:    serviceFlagExternalIPs | serviceFlagRoutable,
			want: "ExternalIPs",
		},
		{
			name: "Test-2",
			s:    serviceFlagNone | serviceFlagRoutable,
			want: "ClusterIP",
		},
		{
			name: "Test-3",
			s:    serviceFlagNodePort | serviceFlagExtLocalScope | serviceFlagRoutable,
			want: "NodePort, Local",
		},
		{
			name: "Test-4",
			s:    serviceFlagExternalIPs | serviceFlagExtLocalScope | serviceFlagRoutable,
			want: "ExternalIPs, Local",
		},
		{
			name: "Test-5",
			s:    serviceFlagLoadBalancer | serviceFlagRoutable,
			want: "LoadBalancer",
		},
		{
			name: "Test-6",
			s:    serviceFlagLoadBalancer,
			want: "LoadBalancer, non-routable",
		},
		{
			name: "Test-7",
			s:    serviceFlagNodePort | serviceFlagIntLocalScope | serviceFlagRoutable,
			want: "NodePort, InternalLocal",
		},
		{
			name: "Test-8",
			s:    serviceFlagExternalIPs | serviceFlagIntLocalScope | serviceFlagRoutable,
			want: "ExternalIPs, InternalLocal",
		},
		{
			name: "Test-9",
			s:    serviceFlagNodePort | serviceFlagExtLocalScope | serviceFlagIntLocalScope | serviceFlagRoutable,
			want: "NodePort, Local, InternalLocal",
		},
		{
			name: "Test-10",
			s:    serviceFlagExternalIPs | serviceFlagExtLocalScope | serviceFlagIntLocalScope | serviceFlagRoutable,
			want: "ExternalIPs, Local, InternalLocal",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServiceNameYAML(t *testing.T) {
	tests := []struct {
		name ServiceName
		want string
	}{
		{
			name: ServiceName{},
			want: "/",
		},
		{
			name: ServiceName{Name: "foo"},
			want: "/foo",
		},
		{
			name: ServiceName{Name: "foo", Namespace: "bar"},
			want: "bar/foo",
		},
		{
			name: ServiceName{Name: "foo", Namespace: "bar", Cluster: "quux"},
			want: "quux/bar/foo",
		},
	}
	for _, test := range tests {
		out, err := yaml.Marshal(test.name)
		if assert.NoError(t, err, "Marshal") {
			s := strings.TrimSpace(string(out))
			assert.Equal(t, test.want, s)

			var name ServiceName
			err := yaml.Unmarshal(out, &name)
			if assert.NoError(t, err, "Unmarshal") {
				assert.True(t, test.name.Equal(name), "Equal")
			}
		}
	}
}

func benchmarkHash(b *testing.B, addr *L3n4Addr) {
	b.ReportAllocs()

	for b.Loop() {
		addr.Hash()
	}
}

func BenchmarkL3n4Addr_Hash_IPv4(b *testing.B) {
	addr := NewL3n4Addr(TCP, cmtypes.MustParseAddrCluster("1.2.3.4"), 8080, ScopeInternal)
	benchmarkHash(b, addr)
}

func BenchmarkL3n4Addr_Hash_IPv6_Short(b *testing.B) {
	addr := NewL3n4Addr(TCP, cmtypes.MustParseAddrCluster("fd00::1:36c6"), 8080, ScopeInternal)
	benchmarkHash(b, addr)
}

func BenchmarkL3n4Addr_Hash_IPv6_Long(b *testing.B) {
	addr := NewL3n4Addr(TCP, cmtypes.MustParseAddrCluster("2001:0db8:85a3::8a2e:0370:7334"), 8080, ScopeInternal)
	benchmarkHash(b, addr)
}

func BenchmarkL3n4Addr_Hash_IPv6_Max(b *testing.B) {
	addr := NewL3n4Addr(TCP, cmtypes.MustParseAddrCluster("1020:3040:5060:7080:90a0:b0c0:d0e0:f000"), 30303, 100)
	benchmarkHash(b, addr)
}

func benchmarkString(b *testing.B, addr *L3n4Addr) {
	b.ReportAllocs()

	var length int
	for b.Loop() {
		length += len(addr.String())
	}
}

func BenchmarkL3n4Addr_String_IPv4(b *testing.B) {
	addr := NewL3n4Addr(TCP, cmtypes.MustParseAddrCluster("192.168.123.210"), 8080, ScopeInternal)
	benchmarkString(b, addr)
}

func BenchmarkL3n4Addr_String_IPv6_Max(b *testing.B) {
	addr := NewL3n4Addr(TCP, cmtypes.MustParseAddrCluster("1020:3040:5060:7080:90a0:b0c0:d0e0:f000"), 30303, 100)
	benchmarkString(b, addr)
}

func benchmarkStringWithProtocol(b *testing.B, addr *L3n4Addr) {
	b.ReportAllocs()

	for b.Loop() {
		addr.StringWithProtocol()
	}
}

func BenchmarkL3n4Addr_StringWithProtocol_IPv4(b *testing.B) {
	addr := NewL3n4Addr(TCP, cmtypes.MustParseAddrCluster("192.168.123.210"), 8080, ScopeInternal)
	benchmarkStringWithProtocol(b, addr)
}

func BenchmarkL3n4Addr_StringWithProtocol_IPv6_Max(b *testing.B) {
	addr := NewL3n4Addr(TCP, cmtypes.MustParseAddrCluster("1020:3040:5060:7080:90a0:b0c0:d0e0:f000"), 30303, 100)
	benchmarkStringWithProtocol(b, addr)
}
