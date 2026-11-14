package service

import (
	"context"
	"errors"
	"sort"

	"yourpm/core/model"
)

// RunOutcome is the result of one request in a collection run.
type RunOutcome struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Method   string             `json:"method"`
	URL      string             `json:"url"`
	Status   int                `json:"status"`
	TimeMs   int64              `json:"timeMs"`
	Tests    []model.TestResult `json:"tests,omitempty"`
	Error    string             `json:"error,omitempty"`
	Failed   bool               `json:"failed"`
	HTTPFail bool               `json:"httpFail"`
	TestFail bool               `json:"testFail"`
}

// RunSummary aggregates a collection or folder run.
type RunSummary struct {
	Total  int          `json:"total"`
	Errors int          `json:"errors"`
	Failed int          `json:"failed"`
	Passed int          `json:"passed"`
	Items  []RunOutcome `json:"items"`
}

// RunCollection executes every request below a collection or folder in tree
// order. It shares the same request path as the UI, so collection variables,
// auth and scripts apply exactly as they do for a single send.
func (s *Service) RunCollection(ctx context.Context, ws, rootID, envID string, overrides map[string]string) (*RunSummary, error) {
	ents, err := s.Store.ListEntities(ws, false)
	if err != nil {
		return nil, err
	}
	byID := map[string]*model.Entity{}
	children := map[string][]*model.Entity{}
	for _, e := range ents {
		byID[e.ID] = e
		children[e.ParentID] = append(children[e.ParentID], e)
	}
	root := byID[rootID]
	if root == nil || (root.Kind != model.KindCollection && root.Kind != model.KindFolder) {
		return nil, errors.New("collection or folder not found")
	}
	for parent := range children {
		sort.SliceStable(children[parent], func(i, j int) bool {
			a, b := children[parent][i], children[parent][j]
			if a.Position == b.Position {
				return a.Name < b.Name
			}
			return a.Position < b.Position
		})
	}
	collectionID := s.CollectionIDFor(ws, rootID)
	sum := &RunSummary{}

	var walk func(parent string)
	walk = func(parent string) {
		for _, e := range children[parent] {
			if ctx.Err() != nil {
				return
			}
			switch e.Kind {
			case model.KindFolder:
				walk(e.ID)
			case model.KindRequest:
				req, err := RequestOf(e)
				if err != nil {
					sum.Total++
					sum.Errors++
					sum.Items = append(sum.Items, RunOutcome{ID: e.ID, Name: e.Name, Error: err.Error(), Failed: true})
					continue
				}
				resp := s.send(ctx, ws, req, envID, collectionID, e.ID, overrides)
				out := RunOutcome{
					ID:     e.ID,
					Name:   e.Name,
					Method: req.Method,
					URL:    resp.URL,
					Status: resp.Status,
					TimeMs: resp.TimeMs,
					Tests:  resp.Tests,
				}
				if out.URL == "" {
					out.URL = req.URL
				}
				out.Error = resp.Error
				out.HTTPFail = resp.Status >= 400
				for _, test := range resp.Tests {
					if !test.Passed {
						out.TestFail = true
					}
				}
				out.Failed = out.Error != "" || out.HTTPFail || out.TestFail
				sum.Total++
				if out.Error != "" {
					sum.Errors++
				}
				if out.Failed {
					sum.Failed++
				} else {
					sum.Passed++
				}
				sum.Items = append(sum.Items, out)
			}
		}
	}
	walk(rootID)
	if err := ctx.Err(); err != nil {
		return sum, err
	}
	return sum, nil
}
