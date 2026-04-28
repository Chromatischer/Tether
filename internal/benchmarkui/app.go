package benchmarkui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"tether/internal/benchmark"
	"tether/internal/tui"
)

var (
	uiBg                = lipgloss.Color("233")
	uiHeaderBg          = lipgloss.Color("232")
	uiAccent            = lipgloss.Color("172")
	uiMuted             = lipgloss.Color("246")
	uiBody              = lipgloss.Color("255")
	uiGood              = lipgloss.Color("114")
	uiWarn              = lipgloss.Color("210")
	styleHeader         = lipgloss.NewStyle().Background(uiHeaderBg).Foreground(uiBody).Bold(true).Padding(0, 1)
	styleBody           = lipgloss.NewStyle().Background(uiBg).Foreground(uiBody)
	styleMuted          = lipgloss.NewStyle().Background(uiBg).Foreground(uiMuted)
	styleAccent         = lipgloss.NewStyle().Background(uiBg).Foreground(uiAccent).Bold(true)
	styleGood           = lipgloss.NewStyle().Background(uiBg).Foreground(uiGood)
	styleWarn           = lipgloss.NewStyle().Background(uiBg).Foreground(uiWarn)
	styleStateScheduled = lipgloss.NewStyle().Foreground(uiMuted)
	styleStateRunning   = lipgloss.NewStyle().Foreground(uiAccent).Bold(true)
	styleStateFinished  = lipgloss.NewStyle().Foreground(uiGood)
	styleStateRating    = lipgloss.NewStyle().Foreground(lipgloss.Color("219")).Bold(true)
	styleStateRated     = lipgloss.NewStyle().Foreground(uiGood).Bold(true)
	styleStateFailed    = lipgloss.NewStyle().Foreground(uiWarn).Bold(true)
	styleScoreStrong    = lipgloss.NewStyle().Foreground(uiGood).Bold(true)
	styleScoreMid       = lipgloss.NewStyle().Foreground(uiAccent)
	styleScoreWeak      = lipgloss.NewStyle().Foreground(uiWarn)
)

type runFinishedMsg struct {
	result *benchmark.RunResult
	err    error
}

type appModel struct {
	runner *benchmark.Runner
	w      int
	h      int
	lines  []string
	result *benchmark.RunResult
	err    error
	done   bool
	msgs   chan tea.Msg
	models []modelView
	tasks  []taskView
	states map[string]map[string]cellView
}

type modelView struct {
	ID    string
	Label string
}

type taskView struct {
	ID    string
	Title string
}

type cellView struct {
	State string
	Err   string
}

func New(runner *benchmark.Runner) tea.Model {
	plan := runner.Plan()
	m := &appModel{
		runner: runner,
		msgs:   make(chan tea.Msg, 128),
		lines:  []string{"Preparing benchmark run..."},
		states: map[string]map[string]cellView{},
	}
	for _, model := range plan.Models {
		label := strings.TrimSpace(model.Label)
		if label == "" {
			label = model.ID
		}
		m.models = append(m.models, modelView{ID: model.ID, Label: label})
	}
	for _, task := range plan.Tasks {
		title := strings.TrimSpace(task.Title)
		if title == "" {
			title = task.ID
		}
		m.tasks = append(m.tasks, taskView{ID: task.ID, Title: title})
	}
	m.initializeStates()
	return m
}

func (m *appModel) Init() tea.Cmd {
	go func() {
		result, err := m.runner.Run(context.Background(), func(ev benchmark.ProgressEvent) {
			line := ev.Stage
			if ev.TaskID != "" {
				line += " task=" + ev.TaskID
			}
			if ev.ModelID != "" {
				line += " model=" + ev.ModelID
			}
			if ev.JudgeID != "" {
				line += " judge=" + ev.JudgeID
			}
			if ev.Message != "" {
				line += " " + strings.TrimSpace(ev.Message)
			}
			if ev.Err != "" {
				line += " err=" + ev.Err
			}
			ev.Message = strings.TrimSpace(line)
			m.msgs <- ev
		})
		m.msgs <- runFinishedMsg{result: result, err: err}
	}()
	return m.waitMsg()
}

func (m *appModel) waitMsg() tea.Cmd {
	return func() tea.Msg {
		return <-m.msgs
	}
}

func (m *appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w = msg.Width
		m.h = msg.Height
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}
	case benchmark.ProgressEvent:
		m.updateAttempt(msg)
		m.lines = append(m.lines, msg.Message)
		if len(m.lines) > 8 {
			m.lines = m.lines[len(m.lines)-8:]
		}
		return m, m.waitMsg()
	case runFinishedMsg:
		m.result = msg.result
		m.err = msg.err
		m.done = true
		return m, nil
	}
	return m, nil
}

func (m *appModel) View() tea.View {
	if m.w <= 0 {
		m.w = 100
	}
	lines := []string{
		styleHeader.Width(m.w).Render("Tether Benchmark"),
		m.renderStatus(),
	}
	if live := m.renderLiveTable(); live != "" {
		lines = append(lines, styleBody.Width(m.w).Render(""))
		lines = append(lines, live)
		lines = append(lines, styleBody.Width(m.w).Render(""))
	}
	for _, line := range m.lines {
		lines = append(lines, styleBody.Width(m.w).Render(line))
	}
	if m.done {
		lines = append(lines, styleBody.Width(m.w).Render(""))
		lines = append(lines, m.renderResult())
	} else {
		lines = append(lines, styleMuted.Width(m.w).Render("Press q to quit"))
	}
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	return v
}

func (m *appModel) renderStatus() string {
	if m.err != nil {
		return styleWarn.Width(m.w).Render("Run failed: " + m.err.Error())
	}
	if m.done && m.result != nil {
		return styleGood.Width(m.w).Render(fmt.Sprintf("Completed in %s. Results saved to %s", m.result.Duration.Round(time.Second), m.result.ResultsPath))
	}
	return styleAccent.Width(m.w).Render("Running benchmark...")
}

func (m *appModel) initializeStates() {
	for _, model := range m.models {
		if _, ok := m.states[model.ID]; !ok {
			m.states[model.ID] = map[string]cellView{}
		}
		for _, task := range m.tasks {
			m.states[model.ID][task.ID] = cellView{State: "SCHEDULED"}
		}
	}
}

func (m *appModel) updateAttempt(ev benchmark.ProgressEvent) {
	if ev.Stage == "run_finished" {
		for _, model := range m.models {
			for _, task := range m.tasks {
				cell := m.cell(model.ID, task.ID)
				if cell.State == "RATING" {
					m.setCell(model.ID, task.ID, "RATED", cell.Err)
				}
			}
		}
		return
	}
	if ev.TaskID == "" {
		return
	}
	switch ev.Stage {
	case "attempt_started":
		m.setCell(ev.ModelID, ev.TaskID, "RUNNING", "")
	case "attempt_finished":
		m.setCell(ev.ModelID, ev.TaskID, "FINISHED", "")
	case "attempt_failed":
		m.setCell(ev.ModelID, ev.TaskID, "FAILED", ev.Err)
	case "judge_started", "judge_model_started":
		m.setTaskRating(ev.TaskID, "RATING")
	case "judge_finished":
		m.setTaskRating(ev.TaskID, "RATED")
	case "judge_model_failed":
		m.setTaskRating(ev.TaskID, "RATING")
	}
}

func (m *appModel) setTaskRating(taskID, state string) {
	for _, model := range m.models {
		cell := m.cell(model.ID, taskID)
		switch cell.State {
		case "FINISHED", "RATING", "RATED":
			m.setCell(model.ID, taskID, state, cell.Err)
		}
	}
}

func (m *appModel) setCell(modelID, taskID, state, err string) {
	if modelID == "" || taskID == "" {
		return
	}
	if _, ok := m.states[modelID]; !ok {
		m.states[modelID] = map[string]cellView{}
	}
	m.states[modelID][taskID] = cellView{State: state, Err: err}
}

func (m *appModel) cell(modelID, taskID string) cellView {
	if byTask, ok := m.states[modelID]; ok {
		if cell, ok := byTask[taskID]; ok {
			return cell
		}
	}
	return cellView{State: "SCHEDULED"}
}

func (m *appModel) renderLiveTable() string {
	if len(m.models) == 0 || len(m.tasks) == 0 {
		return ""
	}
	headers := []string{"Model"}
	for _, task := range m.tasks {
		headers = append(headers, task.ID)
	}
	rows := make([][]string, 0, len(m.models))
	for _, model := range m.models {
		row := []string{model.Label}
		for _, task := range m.tasks {
			cell := m.cell(model.ID, task.ID)
			row = append(row, cell.State)
		}
		rows = append(rows, row)
	}
	return tui.RenderAssistantTable(headers, rows, max(30, m.w-2), func(row, col int, value string) lipgloss.Style {
		if col == 0 {
			return lipgloss.NewStyle().Foreground(uiBody).Bold(true)
		}
		state := "SCHEDULED"
		model := m.models[row]
		task := m.tasks[col-1]
		state = m.cell(model.ID, task.ID).State
		return stateStyle(state)
	})
}

func (m *appModel) renderResult() string {
	if m.result == nil {
		return styleWarn.Width(m.w).Render("No result available")
	}
	headers := []string{"#", "Model", "Overall", "Objective", "Subjective", "Completed", "Cost"}
	rows := make([][]string, 0, len(m.result.ModelSummaries))
	for i, summary := range m.result.ModelSummaries {
		rows = append(rows, []string{
			fmt.Sprintf("%d", i+1),
			summary.ModelLabel,
			fmt.Sprintf("%.3f", summary.AverageOverall),
			fmt.Sprintf("%.3f", summary.AverageObjective),
			fmt.Sprintf("%.3f", summary.AverageSubjective),
			fmt.Sprintf("%d/%d", summary.TasksCompleted, summary.TasksTotal),
			fmt.Sprintf("$%.4f", summary.TotalCost),
		})
	}
	table := tui.RenderAssistantTable(headers, rows, max(30, m.w-2), func(row, col int, value string) lipgloss.Style {
		switch col {
		case 0, 1:
			return lipgloss.NewStyle().Foreground(uiBody)
		case 2, 3, 4:
			var score float64
			_, _ = fmt.Sscanf(value, "%f", &score)
			return scoreStyle(score)
		default:
			return lipgloss.NewStyle().Foreground(uiMuted)
		}
	})
	heading := tui.RenderAssistantRichText("## Final Ranking", max(30, m.w-2))
	footer := styleMuted.Render("Press q to quit")
	return styleBody.Width(m.w).Render(strings.Join([]string{heading, table, footer}, "\n\n"))
}

func stateStyle(state string) lipgloss.Style {
	switch state {
	case "SCHEDULED":
		return styleStateScheduled
	case "RUNNING":
		return styleStateRunning
	case "FINISHED":
		return styleStateFinished
	case "RATING":
		return styleStateRating
	case "RATED":
		return styleStateRated
	case "FAILED":
		return styleStateFailed
	default:
		return lipgloss.NewStyle().Foreground(uiBody)
	}
}

func scoreStyle(v float64) lipgloss.Style {
	switch {
	case v >= 0.75:
		return styleScoreStrong
	case v >= 0.45:
		return styleScoreMid
	default:
		return styleScoreWeak
	}
}
