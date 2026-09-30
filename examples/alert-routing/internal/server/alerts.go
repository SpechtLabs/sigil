package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/alertmanager"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
)

// webhook handles POST /api/v1/alerts, Alertmanager's webhook: it routes
// every firing alert of the batch and acknowledges every resolved one, in
// the webhook's order, and answers with each alert's result.
//
// Alertmanager retries a webhook until it gets a 2xx, so the answer is 200
// once the batch is processed, whatever happened to its alerts: an unowned,
// an invalid and a failed alert are each routed to the fallback and reported
// in its result, and retrying would only route them again. A payload that
// isn't a webhook answers 400, or 413 over the size cap, and a router that
// hasn't loaded its policies 503, so Alertmanager retries once it has. A
// request the client canceled gets 499 and no body; the alerts routed
// before the client left stay routed, and Alertmanager's retry routes the
// batch again.
func (s *Server) webhook(c *gin.Context) {
	snap, ok := s.store.Snapshot()
	if !ok {
		writeError(c, http.StatusServiceUnavailable, errNotLoaded())
		return
	}

	hook, herr := decodeJSON[alertmanager.Webhook](c, lenientFields,
		"send Alertmanager's webhook payload, version 4, with a webhook_configs receiver pointing at /api/v1/alerts")
	if herr != nil {
		writeError(c, bodyStatus(herr), herr)
		return
	}
	if herr := hook.Validate(); herr != nil {
		writeError(c, http.StatusBadRequest, herr)
		return
	}

	s.metrics.ObserveBatch(len(hook.Alerts))
	// One clock reading for the batch, so every alert's firing time is
	// measured from the same moment, the one the webhook arrived.
	now := s.clock.Now()
	ctx := c.Request.Context()

	resp := WebhookResponse{Received: len(hook.Alerts), Results: make([]AlertResult, 0, len(hook.Alerts))}
	for _, a := range hook.Alerts {
		result := AlertResult{Fingerprint: a.Fingerprint, AlertName: a.Labels[alertmanager.LabelAlertName]}
		if !a.Firing() {
			s.metrics.ObserveReceived(telemetry.AlertResolved)
			result.Status = StatusResolved
			resp.Results = append(resp.Results, result)
			continue
		}
		s.metrics.ObserveReceived(telemetry.AlertFiring)

		r := s.route(ctx, snap, s.webhookJob(a, now))
		if r.canceled {
			c.Status(StatusClientClosedRequest)
			return
		}
		result.Status, result.RouteResponse = r.status, &r.resp
		if r.err != nil {
			result.Error = r.err.Error()
		}
		if r.status == StatusRouted {
			resp.Routed++
		}
		resp.Results = append(resp.Results, result)
	}
	c.JSON(http.StatusOK, resp)
}

// webhookJob reads one firing alert of a webhook: its owner from the team
// label and the directory, and the kind's alert, or why it can't be read.
func (s *Server) webhookJob(a alertmanager.Alert, now time.Time) alertJob {
	label := a.Labels[alertmanager.LabelTeam]
	team, owned := s.directory.Lookup(label)
	j := alertJob{
		name:        a.Labels[alertmanager.LabelAlertName],
		severity:    a.Labels[alertmanager.LabelSeverity],
		fingerprint: a.Fingerprint,
		teamLabel:   label,
		owned:       owned,
		team:        team,
	}
	j.alert, j.invalid = alertmanager.Convert(a, now)
	return j
}
