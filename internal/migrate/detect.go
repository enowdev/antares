package migrate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// SourceInfo is one registered source with what was found on disk.
type SourceInfo struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Detected []Detection `json:"detected"`
}

// DetectAll runs every registered source's detection at its default
// locations. A source that is not installed (ErrNotFound, any other error, or
// an empty Root) gets an empty Detected list; detection never fails the whole
// listing.
func DetectAll(ctx context.Context) []SourceInfo {
	out := make([]SourceInfo, 0, len(registry))
	for _, s := range Sources() {
		dets, err := detectSource(ctx, s, "")
		if err != nil && !errors.Is(err, ErrNotFound) {
			slog.Debug("migrate: detect failed", "source", s.ID(), "error", err)
		}
		if dets == nil {
			dets = []Detection{}
		}
		out = append(out, SourceInfo{ID: s.ID(), Name: s.Name(), Detected: dets})
	}
	return out
}

func detectSource(ctx context.Context, s Source, root string) (dets []Detection, err error) {
	defer func() {
		// A source bug must not take the server down with it.
		if r := recover(); r != nil {
			dets, err = nil, fmt.Errorf("detect %s panicked: %v", s.ID(), r)
		}
	}()
	if md, ok := s.(MultiDetector); ok {
		all, err := md.DetectAll(ctx, root)
		var keep []Detection
		for _, d := range all {
			if d.Root != "" {
				keep = append(keep, fill(s, d))
			}
		}
		return keep, err
	}
	d, err := s.Detect(ctx, root)
	if err != nil || d.Root == "" {
		if err == nil {
			err = ErrNotFound
		}
		return nil, err
	}
	return []Detection{fill(s, d)}, nil
}

func fill(s Source, d Detection) Detection {
	if d.Source == "" {
		d.Source = s.ID()
	}
	if d.Name == "" {
		d.Name = s.Name()
	}
	return d
}

// BuildPlan detects source id (at root, or its default locations) and plans
// the install matching profile ("" = the default/first one).
func BuildPlan(ctx context.Context, id, root, profile string, env Env) (Plan, error) {
	s, ok := Lookup(id)
	if !ok {
		return Plan{}, fmt.Errorf("unknown source %q", id)
	}
	dets, err := detectSource(ctx, s, root)
	if len(dets) == 0 {
		if err == nil || errors.Is(err, ErrNotFound) {
			where := "its default location"
			if root != "" {
				where = root
			}
			return Plan{}, fmt.Errorf("%s was not found at %s: %w", s.Name(), where, ErrNotFound)
		}
		return Plan{}, err
	}
	det := dets[0]
	if profile != "" {
		found := false
		for _, d := range dets {
			if strings.EqualFold(d.Profile, profile) {
				det, found = d, true
				break
			}
		}
		if !found {
			return Plan{}, fmt.Errorf("%s has no profile %q", s.Name(), profile)
		}
	}
	plan, err := planSafely(ctx, s, det, env)
	if err != nil {
		return Plan{}, err
	}
	if plan.Detection.Root == "" {
		plan.Detection = det
	}
	if plan.Detection.Summary == "" {
		plan.Detection.Summary = Summarize(plan.Items)
	}
	if plan.Detection.Running {
		hasChannel := false
		for _, it := range plan.Items {
			if it.Category == CatChannel && it.Status != StatusUnsupported {
				hasChannel = true
			}
		}
		if hasChannel && !containsPrefix(plan.Warnings, plan.Detection.Name+" is running") {
			plan.Warnings = append(plan.Warnings, plan.Detection.Name+" is running — stop it before importing its chat channels, or both will answer the same bots.")
		}
	}
	return plan, nil
}

func containsPrefix(list []string, prefix string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func planSafely(ctx context.Context, s Source, det Detection, env Env) (p Plan, err error) {
	defer func() {
		if r := recover(); r != nil {
			p, err = Plan{}, fmt.Errorf("plan %s panicked: %v", s.ID(), r)
		}
	}()
	return s.Plan(ctx, det, env)
}
