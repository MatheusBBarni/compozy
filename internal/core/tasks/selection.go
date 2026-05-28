package tasks

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/compozy/compozy/internal/core/model"
)

// SelectTaskEntries validates an explicit task selection against discovered entries
// and returns entries in caller order.
func SelectTaskEntries(
	entries []model.IssueEntry,
	selectedTasks []string,
	includeCompleted bool,
) ([]model.IssueEntry, error) {
	normalized, err := normalizeSelectedTasks(selectedTasks)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return entries, nil
	}

	selected := make([]model.IssueEntry, 0, len(normalized))
	selectedEntries := make(map[string]string, len(normalized))
	missing := make([]string, 0)
	completed := make([]string, 0)
	duplicateEntries := make([]string, 0)
	for _, selectedTask := range normalized {
		entry, ok, err := findSelectedTaskEntry(entries, selectedTask)
		if err != nil {
			return nil, err
		}
		if !ok {
			missing = append(missing, selectedTask)
			continue
		}
		canonical := canonicalTaskEntryID(entry)
		if previous, exists := selectedEntries[canonical]; exists {
			duplicateEntries = append(duplicateEntries, fmt.Sprintf("%s (same task as %s)", selectedTask, previous))
			continue
		}
		isCompleted, err := entryIsCompleted(entry)
		if err != nil {
			return nil, err
		}
		if isCompleted && !includeCompleted {
			completed = append(completed, selectedTask)
			continue
		}
		selectedEntries[canonical] = selectedTask
		selected = append(selected, entry)
	}
	if len(duplicateEntries) > 0 {
		return nil, fmt.Errorf("duplicate selected task files: %s", strings.Join(duplicateEntries, ", "))
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("selected task files not found: %s", strings.Join(missing, ", "))
	}
	if len(completed) > 0 {
		sort.Strings(completed)
		return nil, fmt.Errorf("selected task files already completed: %s", strings.Join(completed, ", "))
	}
	return selected, nil
}

func normalizeSelectedTasks(selectedTasks []string) ([]string, error) {
	normalized := make([]string, 0, len(selectedTasks))
	seen := make(map[string]struct{}, len(selectedTasks))
	for idx, selectedTask := range selectedTasks {
		trimmed := strings.TrimSpace(filepath.ToSlash(selectedTask))
		if trimmed == "" {
			return nil, fmt.Errorf("selected task at position %d cannot be empty", idx+1)
		}
		if _, ok := seen[trimmed]; ok {
			return nil, fmt.Errorf("duplicate selected task %q", trimmed)
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized, nil
}

func findSelectedTaskEntry(entries []model.IssueEntry, selectedTask string) (model.IssueEntry, bool, error) {
	var matched *model.IssueEntry
	for idx := range entries {
		entry := entries[idx]
		if !taskEntryMatches(entry, selectedTask) {
			continue
		}
		if matched != nil && canonicalTaskEntryID(*matched) != canonicalTaskEntryID(entry) {
			return model.IssueEntry{}, false, fmt.Errorf("selected task %q matches multiple task files", selectedTask)
		}
		matched = &entry
	}
	if matched == nil {
		return model.IssueEntry{}, false, nil
	}
	return *matched, true, nil
}

func taskEntryMatches(entry model.IssueEntry, selectedTask string) bool {
	for _, selector := range TaskEntrySelectors(entry) {
		if selector == selectedTask {
			return true
		}
	}
	return false
}

// TaskEntrySelectors returns accepted selector spellings for a discovered task entry.
func TaskEntrySelectors(entry model.IssueEntry) []string {
	name := filepath.ToSlash(strings.TrimSpace(entry.Name))
	codeFile := filepath.ToSlash(strings.TrimSpace(entry.CodeFile))
	baseName := filepath.Base(name)
	baseCodeFile := strings.TrimSuffix(baseName, filepath.Ext(baseName))
	selectors := []string{name, codeFile, baseName, baseCodeFile}
	unique := selectors[:0]
	seen := make(map[string]struct{}, len(selectors))
	for _, selector := range selectors {
		if selector == "" {
			continue
		}
		if _, ok := seen[selector]; ok {
			continue
		}
		seen[selector] = struct{}{}
		unique = append(unique, selector)
	}
	return unique
}

func canonicalTaskEntryID(entry model.IssueEntry) string {
	if codeFile := filepath.ToSlash(strings.TrimSpace(entry.CodeFile)); codeFile != "" {
		return codeFile
	}
	name := filepath.ToSlash(strings.TrimSpace(entry.Name))
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func entryIsCompleted(entry model.IssueEntry) (bool, error) {
	task, err := ParseTaskFile(entry.Content)
	if err != nil {
		return false, WrapParseError(entry.AbsPath, err)
	}
	return IsTaskCompleted(task), nil
}
