package dialog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"gopkg.in/yaml.v3"

	"github.com/hackafterdark/phosphor/internal/platform/cron"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/list"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/sahilm/fuzzy"
)

const CronJobsID = "cron_jobs"

// ActionRunJob is sent when running a job.
type ActionRunJob struct {
	JobName string
	Prompt  string
}

// ActionEditJob is sent when editing a job.
type ActionEditJob struct {
	JobPath string
}

type Jobs struct {
	com            *common.Common
	help           help.Model
	list           *list.FilterableList
	input          textinput.Model
	jobs           []*JobItemWrapper
	selectedJobInx int

	keyMap struct {
		Select   key.Binding
		Edit     key.Binding
		Next     key.Binding
		Previous key.Binding
		Close    key.Binding
	}
}

type JobItemWrapper struct {
	Name string
	Path string
	Job  *cron.Job
}

var _ Dialog = (*Jobs)(nil)
var _ help.KeyMap = (*Jobs)(nil)

func NewJobs(com *common.Common) (*Jobs, error) {
	j := new(Jobs)
	j.com = com

	// Find JobsDirectory
	cfg := com.Config()
	workingDir := com.Workspace.WorkingDir()

	dir := ".phosphor/jobs"
	if cfg != nil && cfg.Services != nil {
		if cronEntry, ok := cfg.Services["cron"]; ok && cronEntry.JobsDirectory != "" {
			dir = cronEntry.JobsDirectory
		}
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(workingDir, dir)
	}

	var wrappers []*JobItemWrapper
	if _, err := os.Stat(dir); err == nil {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && filepath.Base(path) == "job.md" {
				jobName := filepath.Base(filepath.Dir(path))
				job, err := loadJobFile(path)
				if err == nil {
					wrappers = append(wrappers, &JobItemWrapper{
						Name: jobName,
						Path: path,
						Job:  job,
					})
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	j.jobs = wrappers

	helpModel := help.New()
	helpModel.Styles = com.Styles.DialogHelpStyles()
	j.help = helpModel

	j.list = list.NewFilterableList(jobItems(com.Styles, wrappers...)...)
	j.list.Focus()

	j.input = textinput.New()
	j.input.SetVirtualCursor(false)
	j.input.Placeholder = "Enter job name"
	j.input.SetStyles(com.Styles.TextInput)
	j.input.Focus()

	j.keyMap.Select = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "run job"),
	)
	j.keyMap.Edit = key.NewBinding(
		key.WithKeys("ctrl+e", "e"),
		key.WithHelp("e", "edit job"),
	)
	j.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	j.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	j.keyMap.Close = CloseKey

	return j, nil
}

func loadJobFile(path string) (*cron.Job, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(string(data), "---", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid frontmatter")
	}
	frontmatter := strings.TrimSpace(parts[1])
	content := strings.TrimSpace(strings.Join(parts[2:], "\n"))

	var fm cron.JobFrontMatter
	if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
		return nil, err
	}

	return &cron.Job{
		Name:             fm.Title,
		Schedule:         fm.Schedule,
		Prompt:           strings.TrimSpace(content),
		SessionMode:      fm.SessionMode,
		Delivery:         fm.Delivery,
		SessionID:        fm.SessionID,
		AllowConcurrent:  fm.AllowConcurrent,
		FailureThreshold: fm.FailureThreshold,
	}, nil
}

func (j *Jobs) ID() string {
	return CronJobsID
}

// Cursor implements Dialog.
func (j *Jobs) Cursor() *tea.Cursor {
	if j.input.Focused() {
		return j.input.Cursor()
	}
	return nil
}

// ShortHelp implements help.KeyMap.
func (j *Jobs) ShortHelp() []key.Binding {
	return []key.Binding{
		j.keyMap.Select,
		j.keyMap.Edit,
		j.keyMap.Close,
	}
}

// FullHelp implements help.KeyMap.
func (j *Jobs) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		j.ShortHelp(),
	}
}

func (j *Jobs) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if key.Matches(msg, j.keyMap.Close) {
			return ActionClose{}
		}

		if j.input.Focused() {
			switch {
			case key.Matches(msg, j.keyMap.Previous):
				j.list.Focus()
				if j.list.IsSelectedFirst() {
					j.list.SelectLast()
				} else {
					j.list.SelectPrev()
				}
				j.list.ScrollToSelected()
			case key.Matches(msg, j.keyMap.Next):
				j.list.Focus()
				if j.list.IsSelectedLast() {
					j.list.SelectFirst()
				} else {
					j.list.SelectNext()
				}
				j.list.ScrollToSelected()
			case key.Matches(msg, j.keyMap.Edit):
				if item := j.list.SelectedItem(); item != nil {
					jobItem := item.(*JobItem)
					return ActionEditJob{JobPath: jobItem.Path}
				}
			case key.Matches(msg, j.keyMap.Select):
				if item := j.list.SelectedItem(); item != nil {
					jobItem := item.(*JobItem)
					return ActionRunJob{JobName: jobItem.Name, Prompt: jobItem.Job.Prompt}
				}
			default:
				var cmd tea.Cmd
				j.input, cmd = j.input.Update(msg)
				value := j.input.Value()
				j.list.SetFilter(value)
				j.list.ScrollToTop()
				j.list.SetSelected(0)
				return ActionCmd{cmd}
			}
		}
	}
	return nil
}

func (j *Jobs) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := j.com.Styles
	width := max(0, min(defaultDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(defaultDialogHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()
	j.input.SetWidth(max(0, innerWidth-t.Dialog.InputPrompt.GetHorizontalFrameSize()-1))
	listHeight := height - heightOffset
	listTotalHeight := j.list.TotalHeight()
	listWidth := max(0, innerWidth-3)
	j.list.SetSize(listWidth, listHeight)
	j.help.SetWidth(innerWidth)

	start, end := j.list.VisibleItemIndices()
	if j.selectedJobInx < start || j.selectedJobInx > end {
		j.list.ScrollToSelected()
	}

	var cur *tea.Cursor
	rc := NewRenderContext(t, width)
	rc.Title = "Scheduled Jobs"
	inputView := t.Dialog.InputPrompt.Render(j.input.View())
	cur = j.Cursor()
	rc.AddPart(inputView)

	listView := t.Dialog.List.Height(j.list.Height()).Render(j.list.Render())
	scrollbar := common.Scrollbar(t, listHeight, listTotalHeight, listHeight, j.list.Offset())
	if scrollbar != "" {
		listView = lipgloss.JoinHorizontal(lipgloss.Top, listView, scrollbar)
	}
	rc.AddPart(listView)
	rc.Help = j.help.View(j)

	view := rc.Render()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

type JobItem struct {
	*list.Versioned
	Name    string
	Path    string
	Job     *cron.Job
	t       *styles.Styles
	focused bool
	m       fuzzy.Match
}

var _ list.FilterableItem = (*JobItem)(nil)

func (j *JobItem) Filter() string {
	return j.Job.Name
}

func (j *JobItem) Finished() bool {
	return true
}

func (j *JobItem) SetFocused(focused bool) {
	if j.focused == focused {
		return
	}
	j.focused = focused
	j.Bump()
}

func (j *JobItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(j.m, m) {
		return
	}
	j.m = m
	j.Bump()
}

func (j *JobItem) Render(width int) string {
	var info string
	if j.Job.Schedule != "" {
		info = j.Job.Schedule
	}

	itemStyles := ListItemStyles{
		ItemBlurred:     j.t.Dialog.NormalItem,
		ItemFocused:     j.t.Dialog.SelectedItem,
		InfoTextBlurred: j.t.Dialog.Sessions.InfoBlurred,
		InfoTextFocused: j.t.Dialog.Sessions.InfoFocused,
	}

	return renderItem(itemStyles, j.Job.Name, info, j.focused, width, nil, &j.m)
}

func jobItems(t *styles.Styles, wrappers ...*JobItemWrapper) []list.FilterableItem {
	items := make([]list.FilterableItem, len(wrappers))
	for i, w := range wrappers {
		items[i] = &JobItem{
			Versioned: list.NewVersioned(),
			Name:      w.Name,
			Path:      w.Path,
			Job:       w.Job,
			t:         t,
		}
	}
	return items
}
