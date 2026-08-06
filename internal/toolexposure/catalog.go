package toolexposure

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"frankenstein/internal/toolinvocation"
)

const (
	artifactKind     = "tool_exposure_catalog"
	formatVersion    = "0"
	exposureIDPrefix = "toolexp"
)

type ToolExposureCatalog struct {
	ID    string                          `json:"id"`
	Tools []toolinvocation.ToolDefinition `json:"tools"`
}

func Seed(callable toolinvocation.ToolCatalog) (ToolExposureCatalog, error) {
	if err := validateCallableCatalog(callable); err != nil {
		return ToolExposureCatalog{}, err
	}
	tools, err := canonicalDefinitions(callable.Tools)
	if err != nil {
		return ToolExposureCatalog{}, err
	}
	return catalogWithID(tools)
}

func Advance(base ToolExposureCatalog, delivered []toolinvocation.ToolDefinition) (ToolExposureCatalog, error) {
	if err := validateCatalog(base); err != nil {
		return ToolExposureCatalog{}, err
	}
	tools, err := canonicalDefinitions(base.Tools)
	if err != nil {
		return ToolExposureCatalog{}, err
	}
	indexByID := map[string]int{}
	nameByID := map[string]string{}
	for i, def := range tools {
		indexByID[def.ID] = i
		nameByID[def.ID] = def.Name
	}
	for _, raw := range delivered {
		def, err := toolinvocation.CanonicalDefinition(raw)
		if err != nil {
			return ToolExposureCatalog{}, err
		}
		for existingID, existingName := range nameByID {
			if existingID != def.ID && existingName == def.Name {
				return ToolExposureCatalog{}, fmt.Errorf("tool name %q collides across stable ids", def.Name)
			}
		}
		if pos, ok := indexByID[def.ID]; ok {
			if tools[pos].Revision == def.Revision {
				continue
			}
			tools[pos] = def
			nameByID[def.ID] = def.Name
			continue
		}
		indexByID[def.ID] = len(tools)
		nameByID[def.ID] = def.Name
		tools = append(tools, def)
	}
	return catalogWithID(tools)
}

func Encode(catalog ToolExposureCatalog) ([]byte, error) {
	if err := validateCatalog(catalog); err != nil {
		return nil, err
	}
	tools := catalog.Tools
	if tools == nil {
		tools = []toolinvocation.ToolDefinition{}
	}
	return json.Marshal(encodedCatalog{
		Kind:    artifactKind,
		Version: formatVersion,
		ID:      catalog.ID,
		Tools:   tools,
	})
}

func Decode(raw []byte) (ToolExposureCatalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var encoded encodedCatalog
	if err := decoder.Decode(&encoded); err != nil {
		return ToolExposureCatalog{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return ToolExposureCatalog{}, errors.New("unexpected trailing JSON value")
		}
		return ToolExposureCatalog{}, err
	}
	if encoded.Kind != artifactKind {
		return ToolExposureCatalog{}, fmt.Errorf("unexpected artifact kind %q", encoded.Kind)
	}
	if encoded.Version != formatVersion {
		return ToolExposureCatalog{}, fmt.Errorf("unexpected artifact version %q", encoded.Version)
	}
	catalog := ToolExposureCatalog{ID: encoded.ID, Tools: encoded.Tools}
	if err := validateCatalog(catalog); err != nil {
		return ToolExposureCatalog{}, err
	}
	tools, err := canonicalDefinitions(catalog.Tools)
	if err != nil {
		return ToolExposureCatalog{}, err
	}
	return ToolExposureCatalog{ID: catalog.ID, Tools: tools}, nil
}

type encodedCatalog struct {
	Kind    string                          `json:"kind"`
	Version string                          `json:"version"`
	ID      string                          `json:"id"`
	Tools   []toolinvocation.ToolDefinition `json:"tools"`
}

func validateCallableCatalog(catalog toolinvocation.ToolCatalog) error {
	if strings.TrimSpace(catalog.ID) == "" {
		return errors.New("callable catalog id is required")
	}
	tools, err := canonicalDefinitions(catalog.Tools)
	if err != nil {
		return err
	}
	want, err := toolinvocation.CatalogID(tools)
	if err != nil {
		return err
	}
	if catalog.ID != want {
		return fmt.Errorf("callable catalog id %q does not match contents", catalog.ID)
	}
	return validateDefinitions(tools)
}

func validateCatalog(catalog ToolExposureCatalog) error {
	if !strings.HasPrefix(catalog.ID, exposureIDPrefix+"_") {
		return fmt.Errorf("exposure catalog id %q has wrong namespace", catalog.ID)
	}
	tools, err := canonicalDefinitions(catalog.Tools)
	if err != nil {
		return err
	}
	if err := validateDefinitions(tools); err != nil {
		return err
	}
	if want, err := exposureID(tools); err != nil {
		return err
	} else if catalog.ID != want {
		return fmt.Errorf("exposure catalog id %q does not match contents", catalog.ID)
	}
	return nil
}

func validateDefinitions(defs []toolinvocation.ToolDefinition) error {
	names := map[string]string{}
	ids := map[string]bool{}
	for _, def := range defs {
		if _, err := toolinvocation.CanonicalDefinition(def); err != nil {
			return err
		}
		if ids[def.ID] {
			return fmt.Errorf("duplicate tool id %q", def.ID)
		}
		ids[def.ID] = true
		if existingID, ok := names[def.Name]; ok && existingID != def.ID {
			return fmt.Errorf("tool name %q collides across stable ids", def.Name)
		}
		names[def.Name] = def.ID
	}
	return nil
}

func catalogWithID(tools []toolinvocation.ToolDefinition) (ToolExposureCatalog, error) {
	out := ToolExposureCatalog{Tools: cloneDefinitions(tools)}
	id, err := exposureID(out.Tools)
	if err != nil {
		return ToolExposureCatalog{}, err
	}
	out.ID = id
	return out, nil
}

type exposureIdentity struct {
	Kind    string                          `json:"kind"`
	Version string                          `json:"version"`
	Tools   []toolinvocation.ToolDefinition `json:"tools"`
}

func exposureID(tools []toolinvocation.ToolDefinition) (string, error) {
	canonical, err := json.Marshal(exposureIdentity{
		Kind:    artifactKind,
		Version: formatVersion,
		Tools:   tools,
	})
	if err != nil {
		return "", err
	}
	return toolinvocation.StableID(exposureIDPrefix, canonical), nil
}

func canonicalDefinitions(defs []toolinvocation.ToolDefinition) ([]toolinvocation.ToolDefinition, error) {
	out := make([]toolinvocation.ToolDefinition, len(defs))
	for i, def := range defs {
		canonical, err := toolinvocation.CanonicalDefinition(def)
		if err != nil {
			return nil, err
		}
		out[i] = canonical
	}
	return out, nil
}

func cloneDefinitions(defs []toolinvocation.ToolDefinition) []toolinvocation.ToolDefinition {
	out := make([]toolinvocation.ToolDefinition, len(defs))
	for i, def := range defs {
		out[i] = def
		out[i].InputSchema = append(json.RawMessage(nil), def.InputSchema...)
	}
	return out
}

func decodeJSON(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}
