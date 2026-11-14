package service

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"yourpm/core/exporter"
	"yourpm/core/id"
	"yourpm/core/importer"
	"yourpm/core/model"
)

// ImportPostman imports a Postman collection or environment document.
// It returns the created root entity.
func (s *Service) ImportPostman(ws string, raw []byte) (*model.Entity, error) {
	if len(raw) == 0 {
		return nil, errors.New("empty file")
	}
	// Environments and collections are distinguishable by their top level.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, errors.New("file is not valid JSON")
	}
	if _, isEnv := probe["values"]; isEnv {
		node, err := importer.ParsePostmanEnvironment(raw)
		if err != nil {
			return nil, err
		}
		return s.createNode(ws, "", node, 0)
	}
	node, err := importer.ParsePostmanCollection(raw)
	if err != nil {
		return nil, err
	}
	return s.createNode(ws, "", node, 0)
}

// ImportPostmanFile imports from disk.
func (s *Service) ImportPostmanFile(ws, path string) (*model.Entity, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return s.ImportPostman(ws, raw)
}

func (s *Service) createNode(ws, parentID string, node *importer.Node, index int) (*model.Entity, error) {
	e := &model.Entity{
		ID:        id.New(),
		Workspace: ws,
		Kind:      node.Kind,
		ParentID:  parentID,
		Name:      node.Name,
		Position:  float64(time.Now().UnixMilli()) + float64(index)*0.001,
		Data:      node.Data,
	}
	if err := s.Store.CreateEntity(e); err != nil {
		return nil, err
	}
	for i, child := range node.Children {
		if _, err := s.createNode(ws, e.ID, child, i); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// ExportCollection renders a collection subtree as Postman v2.1 JSON.
func (s *Service) ExportCollection(collection *model.Entity) ([]byte, error) {
	ents, err := s.Store.ListEntities(collection.Workspace, false)
	if err != nil {
		return nil, err
	}
	return exporter.BuildPostman(collection, ents)
}

// ExportEnvironment renders an environment as Postman JSON.
func (s *Service) ExportEnvironment(env *model.Entity) ([]byte, error) {
	return exporter.BuildPostmanEnvironment(env)
}

// ExportCurl renders a request as a cURL command.
func (s *Service) ExportCurl(ws string, req model.RequestData, envID, collectionID string) string {
	v := s.varsFor(ws, envID, collectionID, nil)
	return exporter.Curl(req, v)
}
