package tools

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"

	toml "github.com/pelletier/go-toml/v2"
	gcfg "github.com/zricethezav/gitleaks/v8/config"
	gl "github.com/zricethezav/gitleaks/v8/detect"
)

type customSecretRulesFile struct {
	Rules      []customSecretRule `toml:"rules"`
	Allowlists []customAllowlist  `toml:"allowlists"`
	Allowlist  *customAllowlist   `toml:"allowlist"`
}

type customSecretRule struct {
	ID          string            `toml:"id"`
	Description string            `toml:"description"`
	Path        string            `toml:"path"`
	Regexp      string            `toml:"regex"`
	SecretGroup int               `toml:"secretGroup"`
	Entropy     float64           `toml:"entropy"`
	Keywords    []string          `toml:"keywords"`
	Tags        []string          `toml:"tags"`
	SkipReport  bool              `toml:"skipReport"`
	Required    []customRequired  `toml:"required"`
	Allowlists  []customAllowlist `toml:"allowlists"`
	Allowlist   *customAllowlist  `toml:"allowlist"`
}

type customRequired struct {
	ID            string `toml:"id"`
	WithinLines   *int   `toml:"withinLines"`
	WithinColumns *int   `toml:"withinColumns"`
}

type customAllowlist struct {
	Description string   `toml:"description"`
	Condition   string   `toml:"condition"`
	Commits     []string `toml:"commits"`
	Paths       []string `toml:"paths"`
	Regexp      []string `toml:"regexes"`
	RegexTarget string   `toml:"regexTarget"`
	Stopwords   []string `toml:"stopwords"`
	TargetRules []string `toml:"targetRules"`
}

var secretRulesPath = func() *atomic.Pointer[string] {
	p := &atomic.Pointer[string]{}
	initial := defaultSecretRulesPath()
	p.Store(&initial)
	return p
}()

func SetSecretRulesPath(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultSecretRulesPath()
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	cleaned := filepath.Clean(path)
	secretRulesPath.Store(&cleaned)
}

func SecretRulesPath() string {
	if p := secretRulesPath.Load(); p != nil {
		return *p
	}
	return defaultSecretRulesPath()
}

func newSecretDetector() (*gl.Detector, error) {
	d, err := detectorWithSecretRules(SecretRulesPath())
	if err == nil {
		return d, nil
	}

	slog.Warn("Failed to load custom secret rules; using gitleaks default rules",
		"path", SecretRulesPath(),
		"error", err,
	)
	return defaultSecretDetector()
}

func detectorWithSecretRules(path string) (*gl.Detector, error) {
	base, err := defaultSecretDetector()
	if err != nil {
		return nil, err
	}
	if path == "" {
		return base, nil
	}

	rules, globalAllowlists, err := loadCustomSecretRules(path)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 && len(globalAllowlists) == 0 {
		return base, nil
	}

	cfg := base.Config
	if err := applyCustomSecretRules(&cfg, rules, globalAllowlists); err != nil {
		return nil, err
	}
	return newDetectorFromConfig(cfg), nil
}

func defaultSecretDetector() (*gl.Detector, error) {
	d, err := gl.NewDetectorDefaultConfig()
	if err != nil {
		return nil, err
	}
	configureSecretDetector(d)
	return d, nil
}

func newDetectorFromConfig(cfg gcfg.Config) *gl.Detector {
	d := gl.NewDetector(cfg)
	configureSecretDetector(d)
	return d
}

func configureSecretDetector(d *gl.Detector) {
	d.MaxDecodeDepth = 1
	d.MaxTargetMegaBytes = 20
	d.Verbose = false
}

func defaultSecretRulesPath() string {
	return filepath.Join(".phosphor", "secret-rules.toml")
}

func loadCustomSecretRules(path string) ([]gcfg.Rule, []customAllowlist, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read custom secret rules: %w", err)
	}

	var file customSecretRulesFile
	decoder := toml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&file); err != nil {
		return nil, nil, fmt.Errorf("parse custom secret rules %s: %w", path, err)
	}

	rules := make([]gcfg.Rule, 0, len(file.Rules))
	for _, custom := range file.Rules {
		rule, err := custom.toRule()
		if err != nil {
			return nil, nil, fmt.Errorf("custom secret rule %q: %w", custom.ID, err)
		}
		rules = append(rules, rule)
	}

	globalAllowlists := make([]customAllowlist, 0, len(file.Allowlists)+1)
	globalAllowlists = append(globalAllowlists, file.Allowlists...)
	if file.Allowlist != nil {
		globalAllowlists = append(globalAllowlists, *file.Allowlist)
	}

	return rules, globalAllowlists, nil
}

func applyCustomSecretRules(cfg *gcfg.Config, rules []gcfg.Rule, globalAllowlists []customAllowlist) error {
	if cfg.Rules == nil {
		cfg.Rules = make(map[string]gcfg.Rule)
	}
	if cfg.Keywords == nil {
		cfg.Keywords = make(map[string]struct{})
	}

	for _, rule := range rules {
		if _, ok := cfg.Rules[rule.RuleID]; !ok {
			cfg.OrderedRules = append(cfg.OrderedRules, rule.RuleID)
		}
		cfg.Rules[rule.RuleID] = rule
		for _, keyword := range rule.Keywords {
			if keyword != "" {
				cfg.Keywords[keyword] = struct{}{}
			}
		}
	}

	for _, custom := range globalAllowlists {
		allowlist, err := custom.toAllowlist()
		if err != nil {
			return fmt.Errorf("custom allowlist %q: %w", custom.Description, err)
		}

		targets := cleanStrings(custom.TargetRules)
		if len(targets) == 0 {
			cfg.Allowlists = append(cfg.Allowlists, allowlist)
			continue
		}

		for _, target := range targets {
			rule, ok := cfg.Rules[target]
			if !ok {
				return fmt.Errorf("allowlist %q targets unknown rule %q", custom.Description, target)
			}
			rule.Allowlists = append(rule.Allowlists, allowlist)
			cfg.Rules[target] = rule
		}
	}

	for _, rule := range cfg.Rules {
		for _, required := range rule.RequiredRules {
			if required == nil || strings.TrimSpace(required.RuleID) == "" {
				return fmt.Errorf("rule %q has an empty required rule ID", rule.RuleID)
			}
			if _, ok := cfg.Rules[required.RuleID]; !ok {
				return fmt.Errorf("rule %q requires unknown rule %q", rule.RuleID, required.RuleID)
			}
		}
	}

	for id, rule := range cfg.Rules {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("invalid rule %q: %w", id, err)
		}
		cfg.Rules[id] = rule
	}

	return nil
}

func (c customSecretRule) toRule() (gcfg.Rule, error) {
	rule := gcfg.Rule{
		RuleID:      strings.TrimSpace(c.ID),
		Description: strings.TrimSpace(c.Description),
		Entropy:     c.Entropy,
		SecretGroup: c.SecretGroup,
		Keywords:    lowerStrings(c.Keywords),
		Tags:        cleanStrings(c.Tags),
		SkipReport:  c.SkipReport,
	}

	if c.Regexp != "" {
		re, err := regexp.Compile(c.Regexp)
		if err != nil {
			return gcfg.Rule{}, fmt.Errorf("invalid regex %q: %w", c.Regexp, err)
		}
		rule.Regex = re
	}

	if c.Path != "" {
		re, err := regexp.Compile(c.Path)
		if err != nil {
			return gcfg.Rule{}, fmt.Errorf("invalid path regex %q: %w", c.Path, err)
		}
		rule.Path = re
	}

	allowlists := make([]customAllowlist, 0, len(c.Allowlists)+1)
	allowlists = append(allowlists, c.Allowlists...)
	if c.Allowlist != nil {
		allowlists = append(allowlists, *c.Allowlist)
	}
	for _, custom := range allowlists {
		allowlist, err := custom.toAllowlist()
		if err != nil {
			return gcfg.Rule{}, err
		}
		rule.Allowlists = append(rule.Allowlists, allowlist)
	}

	for _, custom := range c.Required {
		id := strings.TrimSpace(custom.ID)
		if id == "" {
			return gcfg.Rule{}, errors.New("required rule ID is empty")
		}
		rule.RequiredRules = append(rule.RequiredRules, &gcfg.Required{
			RuleID:        id,
			WithinLines:   custom.WithinLines,
			WithinColumns: custom.WithinColumns,
		})
	}

	return rule, nil
}

func (c customAllowlist) toAllowlist() (*gcfg.Allowlist, error) {
	allowlist := &gcfg.Allowlist{
		Description:    strings.TrimSpace(c.Description),
		Commits:        lowerStrings(c.Commits),
		StopWords:      lowerStrings(c.Stopwords),
		RegexTarget:    strings.TrimSpace(c.RegexTarget),
		MatchCondition: parseAllowlistMatchCondition(c.Condition),
	}

	for _, path := range c.Paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		re, err := regexp.Compile(path)
		if err != nil {
			return nil, fmt.Errorf("invalid allowlist path regex %q: %w", path, err)
		}
		allowlist.Paths = append(allowlist.Paths, re)
	}

	for _, expr := range c.Regexp {
		if strings.TrimSpace(expr) == "" {
			continue
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("invalid allowlist regex %q: %w", expr, err)
		}
		allowlist.Regexes = append(allowlist.Regexes, re)
	}

	if err := allowlist.Validate(); err != nil {
		return nil, err
	}
	return allowlist, nil
}

func parseAllowlistMatchCondition(condition string) gcfg.AllowlistMatchCondition {
	switch strings.ToUpper(strings.TrimSpace(condition)) {
	case "AND", "&&":
		return gcfg.AllowlistMatchAnd
	default:
		return gcfg.AllowlistMatchOr
	}
}

func lowerStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, strings.ToLower(s))
	}
	return out
}

func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}
