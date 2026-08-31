package batch

import (
	"context"
	"html/template"
	"log/slog"
)

// NavBar renders the Batch navigation link for the project nav bar.
// Registered via the NavBarItems hook.
func (e *Extension) NavBar(ctx context.Context, project string) (template.HTML, error) {
	return template.HTML(`<a href="/` + project + `/batch">Batch</a>`), nil
}

// DashboardCard renders a "N running batches" card for the project
// dashboard. Not wired to DashboardExtras (users navigate via NavBar).
func (e *Extension) DashboardCard(ctx context.Context, project string) (template.HTML, error) {
	n, err := e.countRunning(ctx, project)
	if err != nil {
		slog.Warn("batch: dashboard card", "project", project, "error", err)
		return "", nil // non-fatal
	}
	if n == 0 {
		return "", nil
	}
	return template.HTML(`<div class="card">
		<h3 style="margin-top:0">Batch</h3>
		<p><a href="/` + project + `/batch" style="color:#7eb8f7">` + string(rune('0'+n)) + ` running batch(es)</a></p>
	</div>`), nil
}
