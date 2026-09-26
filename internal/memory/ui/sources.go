// Package ui holds the provenance data behind the memory surfaces in the TUI. It is
// data and its stable text encoding only: no widget lives here. The tool-result card,
// the assistant-turn pill and the sources panel all read this package, so the answer
// to "where did this memory come from" is one shape rendered in three places rather
// than three shapes that drift apart.
package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hackafterdark/phosphor/internal/memory"
)

// Roles distinguish what happened to a memory, which is a distinction the panels have
// to keep visible. An injected item rides every prompt whether the reader asked for it
// or not; a recalled item is one a search surfaced; an acted item is one a write
// touched. Blurring them is how a stale pin reads like a fresh answer.
const (
	RoleInjected = "injected"
	RoleRecalled = "recalled"
	RoleActed    = "acted"
)

// The card is what the model reads, so its two prose fields are bounded the way the
// prose the memory feature injects everywhere else is bounded. A summary is a handle,
// not an abstraction: a long body must not be able to ride into context behind a search
// hit.
const maxFieldRunes = 280

// Source is one memory as a reader sees it: the claim, plus the fields that let the
// claim be checked. Thread, Origin, Trust and Why are not decoration — a hit that
// cannot cite its own provenance is the thing that reads as context bleed later.
type Source struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Status  string   `json:"status"`
	Thread  string   `json:"thread,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Detail  string   `json:"detail,omitempty"`
	Origin  string   `json:"origin,omitempty"`
	Why     string   `json:"why,omitempty"`
	Trust   float64  `json:"trust"`
	Pinned  bool     `json:"pinned,omitempty"`
	Updated string   `json:"updated,omitempty"`
	Scope   string   `json:"scope,omitempty"`
	Role    string   `json:"role,omitempty"`
}

// FromHit maps a ranked search result to a source. The hit's Why is kept verbatim
// because it is the ranking's own explanation, and the origin rides beside it so a
// high score never stands in for a citation.
func FromHit(h memory.Hit, role string) Source {
	detail := h.Snippet
	if strings.TrimSpace(detail) == "" {
		detail = h.Body
	}
	return Source{
		ID:      h.ID,
		Type:    string(h.Type),
		Status:  string(h.Status),
		Thread:  h.Thread,
		Tags:    h.Tags,
		Summary: oneLine(h.Summary),
		Detail:  oneLine(detail),
		Origin:  h.Source,
		Why:     oneLine(h.Why),
		Trust:   h.Trust,
		Pinned:  h.Pinned,
		Updated: firstNonEmpty(h.Updated, h.Created),
		Scope:   string(h.Scope),
		Role:    role,
	}
}

// FromHits maps a whole result set.
func FromHits(hits []memory.Hit, role string) []Source {
	out := make([]Source, 0, len(hits))
	for _, h := range hits {
		out = append(out, FromHit(h, role))
	}
	return out
}

// FromEntry maps a row the caller already holds, for the panels that list the corpus
// rather than a result set. Why is passed in because the store invents none for an
// unranked read: "listed by thread" is the honest reason.
func FromEntry(e memory.Entry, role, why string) Source {
	detail := e.Body
	if oneLine(e.Summary) == "" {
		detail = ""
	}
	return Source{
		ID:      e.ID,
		Type:    string(e.Type),
		Status:  string(e.Status),
		Thread:  e.Thread,
		Tags:    e.Tags,
		Summary: oneLine(e.Summary),
		Detail:  oneLine(detail),
		Origin:  e.Source,
		Why:     oneLine(why),
		Trust:   e.Trust,
		Pinned:  e.Pinned,
		Updated: firstNonEmpty(e.Updated, e.Created),
		Scope:   string(e.Scope),
		Role:    role,
	}
}

// FromEntries maps a corpus listing.
func FromEntries(entries []memory.Entry, role, why string) []Source {
	out := make([]Source, 0, len(entries))
	for _, e := range entries {
		out = append(out, FromEntry(e, role, why))
	}
	return out
}

// The card's line grammar, written down because the TUI re-reads what the tools write
// and a format that is only implied is a format somebody will change out from under the
// parser. One item per line: the badge in brackets, the id in parentheses, then the
// summary and, when there is one, the detail after an em dash.
const (
	cardIntro     = "Recalled "
	cardReadHint  = "Fetch a full body with memory_read(ids=[...])."
	badgeOpen     = "["
	badgeClose    = "]"
	badgeJoin     = " · "
	idOpen        = " ("
	idClose       = ")"
	trustPrefix   = "trust "
	trustFmt      = "trust %.2f"
	tagPrefix     = "#"
	originPrefix  = "source: "
	whyPrefix     = "why: "
	detailJoin    = " — "
	pinBadge      = "pinned"
	footprintMark = "Injected window: "
	corpusMark    = "Corpus: "
)

// Card renders the block the memory tools return. It is the model's view of a hit and,
// after one round trip through storage, the TUI's too: the pill and the sources panel
// parse these bytes back out of the stored tool result, so the ids have to be in here
// even though a ranked list reads cleaner without them — a card that tells the reader
// to fetch by id and then withholds the id is a dead end.
func Card(count int, sources []Source) string {
	return CardLabeled(cardIntro, count, sources)
}

// CardLabeled is the card under a caller's own intro, for the surfaces that report a
// write rather than a recall: the same rows, the same parse, a verb that matches what
// actually happened to them.
func CardLabeled(intro string, count int, sources []Source) string {
	if count <= 0 {
		count = len(sources)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s%s:\n", intro, countLabel(count))
	sb.WriteString(Rows(sources))
	fmt.Fprintf(&sb, "\n%s", cardReadHint)
	return sb.String()
}

// Header is a card's first line on its own, for a caller that renders Rows itself
// under a verb of its own choosing.
func Header(intro string, count int) string {
	return fmt.Sprintf("%s%s:", intro, countLabel(count))
}

// Rows renders the numbered rows of a card without the header or the read hint, for the
// surfaces that already have their own heading — a write reports one entry it touched,
// and appending "Fetch a full body" to that would be a instruction the reader did not
// ask for.
func Rows(sources []Source) string {
	var sb strings.Builder
	for i, s := range sources {
		sb.WriteString("\n")
		sb.WriteString(ItemLine(i+1, s))
		if line := contentLine(s); line != "" {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		if line := whyLine(s); line != "" {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// ItemLine is one numbered row: the badge carries what the entry is and how far it can
// be trusted, the parenthetical id carries which one it is.
func ItemLine(n int, s Source) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d. %s%s%s%s\n", n, Badge(s), idOpen, s.ID, idClose)
	return sb.String()
}

// Badge is the bracketed classifier. Trust and the pin flag are always present because
// they are the two fields a reader needs in order to discount a line, and a badge that
// hides an unreviewed 0.50 behind a confident summary is how a guess starts a design
// decision.
func Badge(s Source) string {
	var parts []string
	add := func(values ...string) {
		for _, v := range values {
			if v = strings.TrimSpace(v); v != "" {
				parts = append(parts, v)
			}
		}
	}
	add(s.Type, s.Status, s.Thread)
	if len(s.Tags) > 0 {
		add(tagPrefix + strings.Join(s.Tags, " "+tagPrefix))
	}
	add(s.Role, fmt.Sprintf(trustFmt, s.Trust))
	if s.Pinned {
		add(pinBadge)
	}
	return badgeOpen + strings.Join(parts, badgeJoin) + badgeClose
}

// contentLine is the claim itself, with the detail after it when the summary alone
// would have read as a headline.
func contentLine(s Source) string {
	summary := memory.Sanitize(s.Summary)
	detail := memory.Sanitize(s.Detail)
	switch {
	case summary == "" && detail == "":
		return ""
	case summary == "":
		return "   " + detail
	case detail == "" || detail == summary:
		return "   " + summary
	default:
		return "   " + summary + detailJoin + detail
	}
}

// whyLine carries the ranking's reason plus the entry's origin. Both are optional in the
// data but always worth a line: the first says why this surfaced now, the second says
// which turn or file vouched for it.
func whyLine(s Source) string {
	why := memory.Sanitize(s.Why)
	if s.Origin != "" {
		if why != "" {
			why += badgeJoin
		}
		why += originPrefix + s.Origin
	}
	if why == "" {
		return ""
	}
	return "   " + whyPrefix + why
}

// Parse reads a card back out of a stored tool result. It is tolerant by contract: the
// bytes it sees may have been written by an older build, truncated by the message
// store, or may not be a card at all, so it reports what it recognised and the caller
// decides whether that is enough to render anything.
func Parse(out string) (sources []Source, count int, ok bool) {
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || isHeader(line) || strings.HasPrefix(line, cardReadHint) {
			continue
		}
		if n, rest, isItem := splitItemNumber(line); isItem {
			ok = true
			if count == 0 {
				count = n
			}
			sources = append(sources, parseItemLine(rest))
			continue
		}
		// The two follow-on lines belong to the item above them: the claim, and the
		// reason it surfaced. Dropping them here would make the pill expand to a row of
		// ids, which is the one column the reader can least use on its own.
		if len(sources) == 0 {
			continue
		}
		last := &sources[len(sources)-1]
		switch {
		case strings.HasPrefix(line, whyPrefix):
			last.Why, last.Origin = splitWhy(strings.TrimPrefix(line, whyPrefix))
		case last.Summary == "" && last.Detail == "":
			last.Summary, last.Detail = splitContent(line)
		}
	}
	if header, found := headerCount(out); found {
		count = header
	}
	return sources, count, ok
}

// splitWhy separates the reason from the origin the card joined with a middot.
func splitWhy(body string) (why, origin string) {
	origin = ""
	why = strings.TrimSpace(body)
	if i := strings.Index(why, badgeJoin+originPrefix); i >= 0 {
		origin = strings.TrimSpace(strings.TrimPrefix(why[i+len(badgeJoin):], originPrefix))
		why = strings.TrimSpace(why[:i])
		return why, origin
	}
	if strings.HasPrefix(why, originPrefix) {
		return "", strings.TrimSpace(strings.TrimPrefix(why, originPrefix))
	}
	return why, ""
}

// splitContent separates the summary from the body the card joined with an em dash.
func splitContent(line string) (summary, detail string) {
	if i := strings.Index(line, detailJoin); i >= 0 {
		return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+len(detailJoin):])
	}
	return strings.TrimSpace(line), ""
}

// Count is the collapsed pill label for a turn: how many distinct memories the turn
// touched, never their content, so the standing cost of showing provenance is a handful
// of bytes per turn rather than a recap of the corpus.
func Count(sources []Source) string {
	seen := map[string]bool{}
	for _, s := range sources {
		if s.ID != "" {
			seen[s.ID] = true
		}
	}
	switch len(seen) {
	case 0:
		return ""
	case 1:
		return "+1 memory"
	default:
		return fmt.Sprintf("+%d memories", len(seen))
	}
}

// Citation is the one-line form a panel shows when it is collapsed onto a single entry,
// and the form a reply quotes back. The columns are the same ones Expanded discloses, in
// the same order, so the two views of one entry cannot read as two entries.
func (s Source) Citation() string {
	var parts []string
	add := func(values ...string) {
		for _, v := range values {
			if v = strings.TrimSpace(v); v != "" {
				parts = append(parts, v)
			}
		}
	}
	add(s.ID, s.Thread, s.Origin, fmt.Sprintf(trustFmt, s.Trust))
	if s.Pinned {
		parts = append(parts, pinBadge)
	}
	add(memory.Sanitize(s.Why))
	return strings.Join(parts, badgeJoin)
}

// Expanded is the disclosure a pill opens to: id, summary, thread, origin, trust, why,
// in that order, which is the order of decreasing confidence — what it is, what it
// says, who said it, and how much to believe it.
func (s Source) Expanded() string {
	var sb strings.Builder
	sb.WriteString(s.ID)
	if summary := memory.Sanitize(s.Summary); summary != "" {
		fmt.Fprintf(&sb, "\n  %s", summary)
	}
	if s.Thread != "" {
		fmt.Fprintf(&sb, "\n  thread: %s", s.Thread)
	}
	if s.Origin != "" {
		fmt.Fprintf(&sb, "\n  %s%s", originPrefix, s.Origin)
	}
	fmt.Fprintf(&sb, "\n  "+trustFmt, s.Trust)
	if s.Pinned {
		sb.WriteString(badgeJoin + pinBadge)
	}
	if why := memory.Sanitize(s.Why); why != "" {
		fmt.Fprintf(&sb, "\n  %s%s", whyPrefix, why)
	}
	return sb.String()
}

// Footprint is the byte and corpus cost a write reported, read back out of a stored
// result so the budget panel can show what a turn cost without holding state of its own.
type Footprint struct {
	Injected    int
	InjectLimit int
	Active      int
	Pending     int
	Retired     int
}

// ParseFootprint pulls the footprint block back out of a writer's output. Those numbers
// are the point of the block for the agent and the point of it for the user looking at
// the same bytes, so they are parsed rather than re-derived.
func ParseFootprint(out string) (Footprint, bool) {
	var (
		f     Footprint
		found bool
	)
	// The writer emits both halves on one line, so each is located by its marker rather
	// than by the line's start.
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if body, ok := afterMarker(line, footprintMark); ok {
			if used, limit, ok := cutPair(body, "/"); ok {
				f.Injected, _ = strconv.Atoi(firstWord(used))
				f.InjectLimit, _ = strconv.Atoi(firstWord(limit))
				found = true
			}
		}
		if body, ok := afterMarker(line, corpusMark); ok {
			f.Active, _ = strconv.Atoi(countBefore(body, "active"))
			f.Pending, _ = strconv.Atoi(countBefore(body, "pending"))
			f.Retired, _ = strconv.Atoi(countBefore(body, "retired"))
			found = true
		}
	}
	return f, found
}

func countLabel(n int) string {
	if n == 1 {
		return "1 memory"
	}
	return fmt.Sprintf("%d memories", n)
}

func isHeader(line string) bool {
	return strings.HasSuffix(line, "memory:") || strings.HasSuffix(line, "memories:")
}

func headerCount(out string) (int, bool) {
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if !isHeader(line) {
			continue
		}
		for _, word := range strings.Fields(line) {
			if n, err := strconv.Atoi(word); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func splitItemNumber(line string) (int, string, bool) {
	dot := strings.IndexByte(line, '.')
	if dot <= 0 {
		return 0, line, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(line[:dot]))
	if err != nil || n <= 0 {
		return 0, line, false
	}
	return n, strings.TrimSpace(line[dot+1:]), true
}

func parseItemLine(line string) Source {
	var s Source
	open := strings.Index(line, badgeOpen)
	closer := strings.Index(line, badgeClose)
	if open >= 0 && closer > open {
		s = parseBadge(line[open+len(badgeOpen) : closer])
		if id, ok := parenthetical(line[closer+len(badgeClose):]); ok {
			s.ID = id
		}
		return s
	}
	if id, ok := parenthetical(line); ok {
		s.ID = id
	}
	return s
}

func parseBadge(body string) Source {
	var s Source
	for _, part := range strings.Split(body, badgeJoin) {
		part = strings.TrimSpace(part)
		switch {
		case part == "":
		case part == pinBadge:
			s.Pinned = true
		case strings.HasPrefix(part, tagPrefix):
			for _, tag := range strings.Split(strings.TrimPrefix(part, tagPrefix), " ") {
				tag = strings.TrimSpace(strings.TrimPrefix(tag, tagPrefix))
				if tag != "" {
					s.Tags = append(s.Tags, tag)
				}
			}
		case part == RoleInjected || part == RoleRecalled || part == RoleActed:
			s.Role = part
		case strings.HasPrefix(part, trustPrefix):
			s.Trust, _ = strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(part, trustPrefix)), 64)
		case s.Type == "":
			s.Type = part
		case s.Status == "":
			s.Status = part
		case s.Thread == "":
			s.Thread = part
		default:
			s.Role = part
		}
	}
	return s
}

func parenthetical(s string) (string, bool) {
	s = strings.TrimSpace(s)
	open := strings.TrimSpace(idOpen)
	if !strings.HasPrefix(s, open) || !strings.HasSuffix(s, idClose) {
		return "", false
	}
	id := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, open), idClose))
	if id == "" {
		return "", false
	}
	return id, true
}

// cutPair splits "512/2048 bytes." style text on the first separator.
func cutPair(s, sep string) (string, string, bool) {
	i := strings.Index(s, sep)
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+len(sep):], true
}

// afterMarker returns the text following a marker wherever it sits in the line.
func afterMarker(line, marker string) (string, bool) {
	i := strings.Index(line, marker)
	if i < 0 {
		return "", false
	}
	return line[i+len(marker):], true
}

// countBefore reads the number that precedes a label, as in "12 active, 3 pending". It is
// the last token before the label rather than the first of the line, because the labels
// share one line and each counts from where the previous one left off.
func countBefore(body, label string) string {
	i := strings.Index(body, label)
	if i <= 0 {
		return ""
	}
	fields := strings.Fields(strings.TrimRight(body[:i], " ,"))
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

func firstWord(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")), " ")
	r := []rune(s)
	if len(r) > maxFieldRunes {
		return string(r[:maxFieldRunes]) + "…"
	}
	return s
}
