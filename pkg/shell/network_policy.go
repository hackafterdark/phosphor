package shell

import (
	"net"
	"net/url"
	"strings"
)

type NetworkPolicy struct {
	Enabled         bool
	AllowedCommands []string
	HostAllowlist   []string
}

var networkDeniedCIDRs = parseNetworkCIDRs([]string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"::/128",
	"::1/128",
	"fc00::/7",
	"fd00::/8",
	"fe80::/10",
	"ff00::/8",
})

var networkDeniedHosts = map[string]struct{}{
	"localhost":                {},
	"127.0.0.1":                {},
	"169.254.169.254":          {},
	"metadata":                 {},
	"metadata.google.internal": {},
}

func NetworkBlocker(networkCommands []string, policy NetworkPolicy) BlockFunc {
	banned := make(map[string]struct{}, len(networkCommands))
	for _, cmd := range networkCommands {
		if c := normalizeNetworkCommand(cmd); c != "" {
			banned[c] = struct{}{}
		}
	}

	if !policy.Enabled {
		return func(args []string) bool {
			if len(args) == 0 {
				return false
			}
			_, ok := banned[normalizeNetworkCommand(args[0])]
			return ok
		}
	}

	allowed := make(map[string]struct{}, len(policy.AllowedCommands))
	for _, cmd := range policy.AllowedCommands {
		if c := normalizeNetworkCommand(cmd); c != "" {
			allowed[c] = struct{}{}
		}
	}

	hosts := normalizeNetworkHostList(policy.HostAllowlist)

	return func(args []string) bool {
		if len(args) == 0 {
			return false
		}

		cmd := normalizeNetworkCommand(args[0])
		_, isNetwork := banned[cmd]
		_, isAllowed := allowed[cmd]

		if !isNetwork && !isAllowed {
			return false
		}

		if isNetwork && len(allowed) > 0 && !isAllowed {
			return true
		}

		for _, host := range hostsFromCommandArgs(cmd, args[1:]) {
			if networkHostDenied(host) {
				return true
			}
			if !networkHostAllowed(host, hosts) {
				return true
			}
		}

		return false
	}
}

func parseNetworkCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, n, err := net.ParseCIDR(cidr)
		if err == nil && n != nil {
			out = append(out, n)
		}
	}
	return out
}

func normalizeNetworkCommand(cmd string) string {
	cmd = strings.ToLower(strings.TrimSpace(cmd))
	cmd = strings.Replace(cmd, "\\", "/", -1)
	if idx := strings.LastIndexByte(cmd, '/'); idx >= 0 {
		cmd = cmd[idx+1:]
	}
	return strings.TrimSuffix(cmd, ".exe")
}

func normalizeNetworkHostList(list []string) []string {
	out := make([]string, 0, len(list))
	for _, raw := range list {
		raw = strings.ToLower(strings.TrimSpace(raw))
		if raw == "" {
			continue
		}
		if host, ok := hostFromHostPortOption(raw, false); ok {
			out = append(out, host)
			continue
		}
		out = append(out, raw)
	}
	return out
}

func hostsFromCommandArgs(cmd string, args []string) []string {
	if len(args) == 0 {
		return nil
	}

	filtered := make([]string, 0, len(args))
	skip := false
	for _, arg := range args {
		if skip {
			skip = false
			continue
		}
		if isSkippedFlagValue(cmd, strings.ToLower(arg)) {
			skip = true
			continue
		}
		filtered = append(filtered, arg)
	}

	return hostsFromArgv(filtered)
}

func isSkippedFlagValue(cmd string, arg string) bool {
	if !strings.HasPrefix(arg, "-") {
		return false
	}

	name := arg
	if idx := strings.IndexByte(name, '='); idx >= 0 {
		name = name[:idx]
	}

	switch cmd {
	case "curl":
		switch name {
		case "-d", "--data", "--data-binary", "-o", "--output", "--output-dir", "--dump-header", "--trace", "--trace-ascii":
			return true
		}
	case "wget":
		switch name {
		case "-O", "--output-document", "-o", "--output-file", "-i", "--input", "--log-file":
			return true
		}
	case "ssh":
		switch name {
		case "-b", "-c", "-E", "-F", "-i", "-l", "-L", "-o", "-p", "-R":
			return true
		}
	case "scp":
		switch name {
		case "-b", "-c", "-E", "-F", "-i", "-l", "-o", "-p", "-S", "-T":
			return true
		}
	}

	return false
}

func hostsFromArgv(args []string) []string {
	if len(args) == 0 {
		return nil
	}

	var hosts []string
	seen := make(map[string]struct{}, len(args))
	for _, arg := range args {
		for _, host := range hostsFromArg(arg) {
			host = strings.ToLower(strings.TrimSpace(host))
			if host == "" {
				continue
			}
			if _, ok := seen[host]; !ok {
				seen[host] = struct{}{}
				hosts = append(hosts, host)
			}
		}
	}
	return hosts
}

func hostsFromArg(arg string) []string {
	arg = strings.TrimSpace(arg)
	arg = strings.Trim(arg, "'\"")
	if arg == "" {
		return nil
	}

	if strings.HasPrefix(arg, "-") {
		if idx := strings.IndexByte(arg, '='); idx >= 0 && idx+1 < len(arg) {
			return hostsFromArg(arg[idx+1:])
		}
		return nil
	}

	if strings.Contains(arg, "://") {
		if u, err := url.Parse(arg); err == nil && u.Scheme != "" {
			switch strings.ToLower(u.Scheme) {
			case "file", "mailto", "data", "javascript", "about":
				return nil
			}

			var hosts []string
			if u.Host != "" {
				if host, ok := hostFromHostPortOption(stripURLPort(u.Host), false); ok {
					hosts = append(hosts, host)
				}
			}
			return hosts
		}
	}

	if at := strings.LastIndexByte(arg, '@'); at >= 0 && at+1 < len(arg) {
		rest := cutHostAfterAt(arg[at+1:])
		if host, ok := hostFromHostPortOption(rest, false); ok {
			return []string{host}
		}
	}

	if strings.HasPrefix(arg, "[") {
		if end := strings.IndexByte(arg, ']'); end > 1 {
			if host, ok := hostFromHostPortOption(arg[1:end], false); ok {
				return []string{host}
			}
		}
	}

	if strings.HasPrefix(arg, "/") ||
		strings.HasPrefix(arg, "./") ||
		strings.HasPrefix(arg, "~/") ||
		strings.HasPrefix(arg, "..") ||
		strings.HasPrefix(arg, "file:") {
		return nil
	}

	base := arg
	if slash := strings.IndexByte(base, '/'); slash > 0 {
		base = base[:slash]
	}
	if strings.HasSuffix(base, ":") {
		base = strings.TrimSuffix(base, ":")
	}

	if host, ok := hostFromHostPortOption(base, true); ok {
		return []string{host}
	}

	if host, ok := hostFromHostPortOption(arg, true); ok {
		return []string{host}
	}

	return nil
}

func cutHostAfterAt(s string) string {
	if end := strings.IndexByte(s, ']'); end >= 0 {
		if colon := strings.IndexByte(s[end:], ':'); colon > 0 {
			return s[:end+1]
		}
		if slash := strings.IndexByte(s, '/'); slash >= 0 {
			return s[:slash]
		}
		return s
	}

	if slash := strings.IndexByte(s, '/'); slash >= 0 {
		prefix := s[:slash]
		if colon := strings.IndexByte(prefix, ':'); colon > 0 && strings.Count(prefix, ":") == 1 {
			return prefix[:colon]
		}
		return prefix
	}

	if colon := strings.IndexByte(s, ':'); colon > 0 && colon+1 < len(s) {
		port := s[colon+1:]
		if !isNumericPort(port) && strings.Count(s, ":") == 1 && !looksLikeIPv6(s) {
			return s[:colon]
		}
	}

	return s
}

func hostFromHostPortOption(raw string, requireDot bool) (string, bool) {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "'\"")
	if s == "" {
		return "", false
	}

	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		inner := s[1 : len(s)-1]
		if ip := net.ParseIP(inner); ip != nil {
			return strings.ToLower(ip.String()), true
		}
		if isDomainOption(inner, false) {
			return strings.ToLower(inner), true
		}
		return "", false
	}

	if ip := net.ParseIP(s); ip != nil {
		return strings.ToLower(ip.String()), true
	}

	if colon := strings.LastIndexByte(s, ':'); colon > 0 && colon+1 < len(s) && isNumericPort(s[colon+1:]) {
		host := s[:colon]
		if ip := net.ParseIP(host); ip != nil {
			return strings.ToLower(ip.String()), true
		}
		if isDomainOption(host, requireDot) {
			return strings.ToLower(host), true
		}
	}

	if strings.HasSuffix(s, ":") {
		host := strings.TrimSuffix(s, ":")
		if ip := net.ParseIP(host); ip != nil {
			return strings.ToLower(ip.String()), true
		}
		if isDomainOption(host, requireDot) {
			return strings.ToLower(host), true
		}
	}

	if isDomainOption(s, requireDot) {
		return strings.ToLower(s), true
	}

	return "", false
}

func isDomainOption(host string, requireDot bool) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if requireDot && !strings.Contains(host, ".") {
		return false
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}

	for i := 0; i < len(host); i++ {
		c := host[i]
		switch {
		case c == '.':
		case c == '-':
			if i == 0 || i == len(host)-1 {
				return false
			}
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9':
		default:
			return false
		}
	}

	for _, label := range strings.Split(host, ".") {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
	}

	return true
}

func stripURLPort(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return host
	}
	if host[0] == '[' {
		if end := strings.IndexByte(host, ']'); end >= 0 {
			return host[:end+1]
		}
	}
	if idx := strings.LastIndexByte(host, ':'); idx > 0 && idx+1 < len(host) && isNumericPort(host[idx+1:]) {
		return host[:idx]
	}
	return host
}

func isNumericPort(port string) bool {
	if port == "" || len(port) > 5 {
		return false
	}
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	return true
}

func looksLikeIPv6(s string) bool {
	return strings.Count(s, ":") > 1 || strings.HasPrefix(s, ":")
}

func networkHostDenied(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return false
	}
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}

	if _, ok := networkDeniedHosts[h]; ok {
		return true
	}
	if strings.HasSuffix(h, ".localhost") {
		return true
	}

	ip := parseHostIP(h)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	for _, n := range networkDeniedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func networkHostAllowed(host string, allowlist []string) bool {
	if len(allowlist) == 0 {
		return true
	}

	h := strings.ToLower(strings.TrimSpace(host))
	if ip := parseHostIP(h); ip != nil {
		h = strings.ToLower(ip.String())
	}

	for _, raw := range allowlist {
		p := strings.ToLower(strings.TrimSpace(raw))
		if p == "" {
			continue
		}

		if _, n, err := net.ParseCIDR(p); err == nil && n != nil {
			if ip := parseHostIP(h); ip != nil && n.Contains(ip) {
				return true
			}
			continue
		}

		if strings.HasPrefix(p, "[") && strings.HasSuffix(p, "]") {
			p = p[1 : len(p)-1]
		}

		if strings.HasPrefix(p, "*.") {
			suffix := p[2:]
			if h == suffix || strings.HasSuffix(h, "."+suffix) {
				return true
			}
			continue
		}

		if strings.HasPrefix(p, ".") {
			suffix := p[1:]
			if h == suffix || strings.HasSuffix(h, "."+suffix) {
				return true
			}
			continue
		}

		if h == p {
			return true
		}
	}

	return false
}

func parseHostIP(host string) net.IP {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if len(ip) == 4 {
		return ip
	}
	if s := ip.String(); strings.HasPrefix(s, "::ffff:") {
		if v4 := net.ParseIP(strings.TrimPrefix(s, "::ffff:")); v4 != nil {
			return v4
		}
	}
	return ip
}
