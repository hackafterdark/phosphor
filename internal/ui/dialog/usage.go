package dialog

import (
	"context"
	"database/sql"
	"fmt"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ntcharts/barchart"
	"github.com/NimbleMarkets/ntcharts/canvas"
	"github.com/NimbleMarkets/ntcharts/canvas/runes"
	"github.com/hackafterdark/phosphor/internal/db"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	v1lipgloss "github.com/charmbracelet/lipgloss"
	uv "github.com/charmbracelet/ultraviolet"
)

// UsageID is the identifier for the usage stats dialog.
const UsageID = "usage"

// LoadUsageDataMsg is an internal message that triggers data loading.
type LoadUsageDataMsg struct{}

// TimeRange represents a selectable time range for usage stats.
type TimeRange int

const (
	TimeRange1Day TimeRange = iota
	TimeRange7Days
	TimeRange30Days
	TimeRange60Days
	TimeRange90Days
)

func (tr TimeRange) String() string {
	switch tr {
	case TimeRange1Day:
		return "1D"
	case TimeRange7Days:
		return "7D"
	case TimeRange30Days:
		return "30D"
	case TimeRange60Days:
		return "60D"
	case TimeRange90Days:
		return "90D"
	default:
		return "30D"
	}
}

func (tr TimeRange) days() int {
	switch tr {
	case TimeRange1Day:
		return 1
	case TimeRange7Days:
		return 7
	case TimeRange30Days:
		return 30
	case TimeRange60Days:
		return 60
	case TimeRange90Days:
		return 90
	default:
		return 30
	}
}

// UsageStats represents a single day of usage data.
type UsageStats struct {
	Day              string
	DaysAgo          int
	PromptTokens     int64
	CompletionTokens int64
}

// Usage represents the usage stats dialog.
type Usage struct {
	com               *common.Common
	selectedTimeRange TimeRange
	usageData         []UsageStats
	chart             barchart.Model
	chartReady        bool
	chartError        error
	windowWidth       int
	windowHeight      int
	dialogWidth       int
	dialogHeight      int
	dateRange         string

	styles *styles.Styles
}

// usageDialogMaxWidth is the maximum width for the usage stats dialog.
const usageDialogMaxWidth = 100

// usageDialogMaxHeight is the maximum height for the usage stats dialog.
const usageDialogMaxHeight = 40

var _ Dialog = (*Usage)(nil)

// NewUsage creates a new usage stats dialog.
func NewUsage(com *common.Common) *Usage {
	return &Usage{
		com:               com,
		selectedTimeRange: TimeRange30Days,
		styles:            com.Styles,
	}
}

// ID implements Dialog.
func (u *Usage) ID() string {
	return UsageID
}

// HandleMsg implements [Dialog].
func (u *Usage) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		u.windowWidth = msg.Width
		u.windowHeight = msg.Height
		return nil
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, CloseKey):
			return ActionClose{}
		case key.Matches(msg, key.NewBinding(key.WithKeys("left"))),
			key.Matches(msg, key.NewBinding(key.WithKeys("shift+tab"))):
			u.prevTimeRange()
		case key.Matches(msg, key.NewBinding(key.WithKeys("right"))),
			key.Matches(msg, key.NewBinding(key.WithKeys("tab"))):
			u.nextTimeRange()
		}
	case LoadUsageDataMsg:
		u.loadUsageData()
	}
	return nil
}

// Draw implements Dialog.
func (u *Usage) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := u.styles
	u.dialogWidth = max(0, min(usageDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	u.dialogHeight = max(0, min(usageDialogMaxHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))

	rc := NewRenderContext(t, u.dialogWidth)
	rc.Title = "Usage Stats"

	if !u.chartReady {
		if u.chartError != nil {
			rc.AddPart(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("Error: " + u.chartError.Error()))
		} else {
			rc.AddPart(t.Dialog.InputPrompt.Render("Loading usage data..."))
		}
		DrawCenter(scr, area, rc.Render())
		return nil
	}

	// Spacing above the time range selector
	rc.AddPart(" ")

	// Time range selector
	rc.AddPart(u.drawTimeRangeText())

	// Spacing below the time range selector (above date range)
	rc.AddPart(" ")

	// Date range header
	if u.dateRange != "" {
		rc.AddPart(lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Render("  " + u.dateRange))
	}

	// Spacing below date range (above legend)
	rc.AddPart(" ")

	// Legend
	inColor := u.themeColor(u.styles.WorkingGradFromColor)
	outColor := u.themeColor(u.styles.WorkingGradToColor)
	rc.AddPart("  Tokens In      " + v1lipgloss.NewStyle().Foreground(inColor).Render("█"))
	rc.AddPart("  Tokens Out     " + v1lipgloss.NewStyle().Foreground(outColor).Render("█"))

	// Spacing below legend (above chart)
	rc.AddPart(" ")

	// Chart
	chartLines := strings.Split(u.chart.View(), "\n")
	for i, line := range chartLines {
		chartLines[i] = "  " + line
	}
	rc.AddPart(strings.Join(chartLines, "\n"))

	// X-axis subtitle (centered and directly below chart)
	dialogStyle := u.styles.Dialog.View
	usableWidth := u.dialogWidth - dialogStyle.GetHorizontalFrameSize()
	graphWidth := usableWidth - 4
	if graphWidth < 10 {
		graphWidth = 10
	}
	rc.AddPart("  " + lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Width(graphWidth).
		Align(lipgloss.Center).
		Render("days ago"))

	// Spacing below x-axis subtitle (above totals summary)
	rc.AddPart(" ")

	// Total tokens summary
	var totalPrompt, totalCompletion int64
	for _, d := range u.usageData {
		totalPrompt += d.PromptTokens
		totalCompletion += d.CompletionTokens
	}
	total := totalPrompt + totalCompletion

	rc.AddPart(fmt.Sprintf("  Total: %s tokens", formatTokenCount(total)))
	rc.AddPart("  In: " + formatTokenCount(totalPrompt) + "  Out: " + formatTokenCount(totalCompletion))

	// Spacing below totals summary (above help line)
	rc.AddPart(" ")

	// Help line
	rc.AddPart(lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Render("  ←/→/Tab: cycle time range  •  Esc: close"))

	DrawCenter(scr, area, rc.Render())
	return nil
}

func (u *Usage) drawTimeRangeText() string {
	var ranges []string
	for _, tr := range []TimeRange{TimeRange1Day, TimeRange7Days, TimeRange30Days, TimeRange60Days, TimeRange90Days} {
		if tr == u.selectedTimeRange {
			ranges = append(ranges, lipgloss.NewStyle().
				Background(u.styles.Dialog.InputPrompt.GetBackground()).
				Foreground(u.styles.Dialog.InputPrompt.GetForeground()).
				Bold(true).
				Render(" "+tr.String()+" "))
		} else {
			ranges = append(ranges, lipgloss.NewStyle().
				Foreground(lipgloss.Color("240")).
				Render(" "+tr.String()+" "))
		}
	}
	return "  " + strings.Join(ranges, "  ")
}

// GetUsageStats returns the current usage data.
func (u *Usage) GetUsageStats() []UsageStats {
	return slices.Clone(u.usageData)
}

// GetSelectedTimeRange returns the currently selected time range.
func (u *Usage) GetSelectedTimeRange() TimeRange {
	return u.selectedTimeRange
}

func (u *Usage) nextTimeRange() {
	u.selectedTimeRange = (u.selectedTimeRange + 1) % 5
	u.loadUsageData()
}

func (u *Usage) prevTimeRange() {
	u.selectedTimeRange = (u.selectedTimeRange - 1 + 5) % 5
	u.loadUsageData()
}

func (u *Usage) loadUsageData() {
	if u.com.DB() == nil {
		u.chartReady = false
		u.chartError = fmt.Errorf("database not available")
		return
	}

	q := db.New(u.com.DB())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	daysStr := "-" + strconv.Itoa(u.selectedTimeRange.days()) + " days"
	rows, err := q.GetUsageByDayRange(ctx, sql.NullString{String: daysStr, Valid: true})
	if err != nil {
		u.chartReady = false
		u.chartError = fmt.Errorf("failed to fetch usage data: %w", err)
		return
	}

	u.usageData = make([]UsageStats, 0, len(rows))
	for _, row := range rows {
		day, _ := row.Day.(string)
		promptTokens := int64(0)
		completionTokens := int64(0)
		if row.PromptTokens.Valid {
			promptTokens = int64(row.PromptTokens.Float64)
		}
		if row.CompletionTokens.Valid {
			completionTokens = int64(row.CompletionTokens.Float64)
		}
		u.usageData = append(u.usageData, UsageStats{
			Day:              day,
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
		})
	}

	// Fill in missing days with zero values so the chart shows the full time range.
	u.usageData = u.fillMissingDays()

	// Set date range header.
	u.setDateRange()

	// Aggregate into weekly buckets for 30D+ ranges.
	u.usageData = u.aggregateWeekly()

	u.buildChart()
	u.chartReady = true
	u.chartError = nil
}

func (u *Usage) themeColor(c color.Color) v1lipgloss.Color {
	if c == nil {
		return v1lipgloss.Color("")
	}
	r, g, b, _ := c.RGBA()
	return v1lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8))
}

func (u *Usage) buildChart() {
	if len(u.usageData) == 0 {
		return
	}

	// Determine chart dimensions based on constrained dialog size.
	// Reserve space for title, time range selector, date range, legend,
	// summary, and help lines. Leave margins around the chart.
	dialogStyle := u.styles.Dialog.View
	usableWidth := u.dialogWidth - dialogStyle.GetHorizontalFrameSize()
	graphWidth := usableWidth - 4
	if graphWidth < 10 {
		graphWidth = 10
	}

	usableHeight := u.dialogHeight - dialogStyle.GetVerticalFrameSize()
	// Reserve 16 lines for title, selector, header, legend, spacers, help, etc.
	graphHeight := usableHeight - 16
	if graphHeight < 6 {
		graphHeight = 6
	}

	// Limit the number of bars to fit the chart width
	data := u.usageData
	maxBars := graphWidth / 2
	if len(data) > maxBars {
		data = data[len(data)-maxBars:]
	}

	ch := barchart.New(graphWidth, graphHeight)
	ch.AutoMaxValue = true
	ch.AutoBarWidth = true
	ch.SetShowAxis(true)
	ch.AxisStyle = v1lipgloss.NewStyle().Foreground(v1lipgloss.Color("15"))
	ch.LabelStyle = v1lipgloss.NewStyle().Foreground(v1lipgloss.Color("15"))

	inColor := u.themeColor(u.styles.WorkingGradFromColor)
	outColor := u.themeColor(u.styles.WorkingGradToColor)

	for _, d := range data {
		bar := barchart.BarData{
			Label: strconv.Itoa(d.DaysAgo),
			Values: []barchart.BarValue{
				{
					Name:  "Tokens In",
					Value: float64(d.PromptTokens),
					Style: v1lipgloss.NewStyle().Foreground(inColor),
				},
				{
					Name:  "Tokens Out",
					Value: float64(d.CompletionTokens),
					Style: v1lipgloss.NewStyle().Foreground(outColor),
				},
			},
		}
		ch.Push(bar)
	}

	ch.Draw()
	u.chart = ch
	u.postProcessChart()
}

func (u *Usage) postProcessChart() {
	if u.chart.Height() < 3 {
		return
	}
	originY := u.chart.Height() - 2
	canvasModel := &u.chart.Canvas
	sf := u.chart.Scale()

	inColor := u.themeColor(u.styles.WorkingGradFromColor)
	outColor := u.themeColor(u.styles.WorkingGradToColor)

	// Scan the canvas columns from left to right.
	for x := 0; x < canvasModel.Width(); x++ {
		// Get BarData to determine if a bar is drawn in this column.
		barData := u.chart.BarDataFromPoint(canvas.Point{X: x, Y: originY - 1})
		if barData.Label == "" {
			continue
		}

		daysAgo, err := strconv.Atoi(barData.Label)
		if err != nil {
			continue
		}

		// Find the matching UsageStats in u.usageData.
		var d UsageStats
		found := false
		for _, stat := range u.usageData {
			if stat.DaysAgo == daysAgo {
				d = stat
				found = true
				break
			}
		}
		if !found {
			// Also support weekly aggregated view if u.usageData has it.
			// In weekly mode, d.DaysAgo matches the first day of that week.
			// Let's search if daysAgo belongs to the week starting at stat.DaysAgo.
			// Since we grouped by daysAgo / 7 in aggregateWeekly:
			for _, stat := range u.usageData {
				if stat.DaysAgo/7 == daysAgo/7 {
					d = stat
					found = true
					break
				}
			}
		}
		if !found {
			continue
		}

		hPurple := float64(d.PromptTokens) * sf
		hTotal := float64(d.PromptTokens+d.CompletionTokens) * sf

		// Re-draw the column using our custom logic that handles stacked backgrounds.
		for i := 0; i < originY; i++ {
			y := originY - 1 - i
			low := float64(i)
			high := float64(i + 1)

			var r rune
			var style v1lipgloss.Style

			if high <= hPurple {
				// Fully purple (Tokens In)
				r = runes.FullBlock
				style = v1lipgloss.NewStyle().Foreground(inColor)
			} else if low >= hTotal {
				// Fully empty
				r = runes.Null
				style = v1lipgloss.NewStyle()
			} else if low >= hPurple && high <= hTotal {
				// Fully pink (Tokens Out)
				r = runes.FullBlock
				style = v1lipgloss.NewStyle().Foreground(outColor)
			} else if hPurple >= low && hPurple < high {
				// Transition from purple to pink/empty
				purpleFraction := hPurple - low
				r = runes.LowerBlockElementFromFloat64(purpleFraction)
				style = v1lipgloss.NewStyle().Foreground(inColor)
				if hTotal > hPurple {
					style = style.Background(outColor)
				}
			} else if low >= hPurple && hTotal >= low && hTotal < high {
				// Transition from pink to empty
				pinkFraction := hTotal - low
				r = runes.LowerBlockElementFromFloat64(pinkFraction)
				style = v1lipgloss.NewStyle().Foreground(outColor)
			}

			if r == runes.Null {
				canvasModel.SetCell(canvas.Point{X: x, Y: y}, canvas.NewCell(0))
			} else {
				canvasModel.SetCell(canvas.Point{X: x, Y: y}, canvas.NewCellWithStyle(r, style))
			}
		}
	}
}

// fillMissingDays adds zero-value entries for any days in the selected range
// that have no data, so the chart shows the full time span.
func (u *Usage) fillMissingDays() []UsageStats {
	days := u.selectedTimeRange.days()
	now := time.Now()

	// Build a set of existing days from the query result.
	existing := make(map[string]bool, len(u.usageData))
	for _, d := range u.usageData {
		existing[d.Day] = true
	}

	// Generate all days in the range (oldest first).
	result := make([]UsageStats, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := now.AddDate(0, 0, -i)
		dayStr := day.Format("2006-01-02")
		if existing[dayStr] {
			// Find the matching data entry.
			for _, d := range u.usageData {
				if d.Day == dayStr {
					d.DaysAgo = i
					result = append(result, d)
					break
				}
			}
		} else {
			result = append(result, UsageStats{Day: dayStr, DaysAgo: i})
		}
	}
	return result
}

// setDateRange sets the date range header string.
func (u *Usage) setDateRange() {
	days := u.selectedTimeRange.days()
	now := time.Now()
	endDate := now.Format("Jan 2")
	startDate := now.AddDate(0, 0, -(days - 1)).Format("Jan 2")
	u.dateRange = fmt.Sprintf("%s – %s", startDate, endDate)
}

// aggregateWeekly groups daily data into weekly buckets for 30D+ ranges.
func (u *Usage) aggregateWeekly() []UsageStats {
	days := u.selectedTimeRange.days()
	if days <= 7 {
		// Return a copy to avoid shared backing array issues with slices.Reverse.
		result := make([]UsageStats, len(u.usageData))
		copy(result, u.usageData)
		return result
	}

	// Group by week (7-day periods).
	weeks := make(map[int]*UsageStats)
	var weekKeys []int

	for _, d := range u.usageData {
		weekNum := d.DaysAgo / 7
		if _, ok := weeks[weekNum]; !ok {
			weeks[weekNum] = &UsageStats{
				Day:     d.Day,
				DaysAgo: d.DaysAgo,
			}
			weekKeys = append(weekKeys, weekNum)
		}
		weeks[weekNum].PromptTokens += d.PromptTokens
		weeks[weekNum].CompletionTokens += d.CompletionTokens
	}

	// Sort by week number and build result in descending order (oldest week first).
	slices.Sort(weekKeys)
	slices.Reverse(weekKeys)
	result := make([]UsageStats, 0, len(weekKeys))
	for _, wk := range weekKeys {
		result = append(result, *weeks[wk])
	}
	return result
}

func formatTokenCount(n int64) string {
	if n >= 1_000_000_000 {
		return strconv.FormatInt(n/1_000_000_000, 10) + "B"
	}
	if n >= 1_000_000 {
		return strconv.FormatInt(n/1_000_000, 10) + "M"
	}
	if n >= 1_000 {
		return strconv.FormatInt(n/1_000, 10) + "K"
	}
	return strconv.FormatInt(n, 10)
}
