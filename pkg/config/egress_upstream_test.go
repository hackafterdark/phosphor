package config

import "testing"

func TestValidateEgressUpstreamProxy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cfg     EgressIsolationConfig
		wantErr bool
	}{
		{
			name: "clean upstream proxy",
			cfg:  EgressIsolationConfig{UpstreamProxy: "http://proxy.corp:3128"},
		},
		{
			name:    "inline credentials rejected",
			cfg:     EgressIsolationConfig{UpstreamProxy: "http://user:pass@proxy.corp:3128"},
			wantErr: true,
		},
		{
			name:    "non-http scheme rejected",
			cfg:     EgressIsolationConfig{UpstreamProxy: "socks5://proxy.corp:1080"},
			wantErr: true,
		},
		{
			name:    "invalid url rejected",
			cfg:     EgressIsolationConfig{UpstreamProxy: "not a url"},
			wantErr: true,
		},
		{
			name: "absolute ca bundle accepted",
			cfg:  EgressIsolationConfig{CABundle: t.TempDir() + "/corp-ca.pem"},
		},
		{
			name:    "relative ca bundle rejected",
			cfg:     EgressIsolationConfig{CABundle: "corp-ca.pem"},
			wantErr: true,
		},
		{
			name: "empty fields accepted",
			cfg:  EgressIsolationConfig{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.ValidateEgressUpstreamProxy()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEgressUpstreamProxyNilConfig(t *testing.T) {
	t.Parallel()

	var cfg *EgressIsolationConfig
	if err := cfg.ValidateEgressUpstreamProxy(); err != nil {
		t.Fatalf("nil config must pass validation, got %v", err)
	}
}
