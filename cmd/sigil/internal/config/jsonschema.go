package config

import (
	"encoding/json"
	"fmt"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/lint"
)

// SchemaURL is where the docs site publishes the configuration file's
// JSON Schema, and the schema's $id.
const SchemaURL = "https://sigil.specht-labs.de/schema/config.json"

// The JSON Schema types the configuration's schema uses.
const (
	typeString = "string"
	typeArray  = "array"
	typeObject = "object"
	typeNull   = "null"
)

// jsonSchema is one JSON Schema (draft 2020-12) node, with the keywords
// the configuration's schema uses, in the order it prints them.
type jsonSchema struct {
	Schema               string                 `json:"$schema,omitempty"`
	ID                   string                 `json:"$id,omitempty"`
	Title                string                 `json:"title,omitempty"`
	Description          string                 `json:"description,omitempty"`
	Type                 any                    `json:"type,omitempty"` // a type, or a list of them
	Enum                 []string               `json:"enum,omitempty"`
	MinLength            int                    `json:"minLength,omitempty"`
	Pattern              string                 `json:"pattern,omitempty"`
	AnyOf                []*jsonSchema          `json:"anyOf,omitempty"`
	Items                *jsonSchema            `json:"items,omitempty"`
	Required             []string               `json:"required,omitempty"`
	Properties           map[string]*jsonSchema `json:"properties,omitempty"`
	AdditionalProperties *bool                  `json:"additionalProperties,omitempty"`
}

// Schema returns the configuration file's JSON Schema (draft 2020-12),
// indented, with a trailing newline. One schema covers the three formats:
// editors apply it to JSON natively, to YAML through yaml-language-server
// and to TOML through Taplo. It's built from the keys the parser accepts
// and the lints [lint.All] lists, so it can't drift from them; the docs
// site serves a copy, which a test keeps current.
//
// The schema is stricter than the parser in one respect: it wants
// strings where the parser also reads a number or a boolean as one.
func Schema() ([]byte, humane.Error) {
	closed := false
	levels := []string{lint.Off.String(), lint.Warn.String(), lint.Error.String()}
	lints := map[string]*jsonSchema{}
	for _, l := range lint.All {
		lints[l.Name] = &jsonSchema{Description: fmt.Sprintf("%s Default %s.", l.Summary, l.Default), Enum: levels}
	}
	entry := &jsonSchema{
		Description:          "A policy every root it applies to must invoke unconditionally.",
		Type:                 typeObject,
		Required:             []string{keyPolicy},
		AdditionalProperties: &closed,
		Properties: map[string]*jsonSchema{
			keyPolicy: {
				Description: "The required policy's name: one name, not a pattern.",
				Type:        typeString,
				MinLength:   1,
				Pattern:     `^[^*]+$`,
			},
			keyTrusted: oneOrMany("Files and directories the required policy comes from, as --trusted reads them.",
				"A file or directory, relative to the configuration file."),
			keyRoots: oneOrMany("Name patterns of the policies the requirement applies to, as --policy takes them. Without it, every policy of the required policy's kind that no other policy invokes.",
				`A policy name or pattern, such as "payments.*".`),
		},
	}
	schema := &jsonSchema{
		Schema:               "https://json-schema.org/draft/2020-12/schema",
		ID:                   SchemaURL,
		Title:                "Sigil configuration file",
		Description:          "The configuration of a Sigil policy repository: sigil.yaml, sigil.json or sigil.toml, or the same with a leading dot. Paths are relative to the file's directory. See https://sigil.specht-labs.de/reference/config/.",
		Type:                 typeObject,
		AdditionalProperties: &closed,
		Properties: map[string]*jsonSchema{
			schemaKey: {Description: "The JSON Schema the file follows, for editors. The tools ignore it.", Type: typeString},
			keyKinds: oneOrMany("Kind files outside the paths a command reads, which every policy command loads as if named with --kind.",
				"A kind file, relative to the configuration file."),
			keyTrusted: oneOrMany("Files and directories read as trusted, as --trusted reads them: their documents resolve first, and no other document may take a name they define.",
				"A file or directory, relative to the configuration file."),
			keyRequire: {
				Description: "The policies sigil check enforces, one entry each. A policy can be required once.",
				Type:        []string{typeArray, typeNull},
				Items:       entry,
			},
			keyLints: {
				Description:          "The level of each lint: off, warn or error. Lints the file doesn't name keep their defaults.",
				Type:                 []string{typeObject, typeNull},
				AdditionalProperties: &closed,
				Properties:           lints,
			},
		},
	}
	out, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, humane.Wrap(err, "the configuration's JSON Schema couldn't be encoded", "this is a bug in sigil; report it")
	}
	return append(out, '\n'), nil
}

// oneOrMany is the schema of a key that takes a non-empty string or a
// list of them, as kinds, trusted and roots do.
func oneOrMany(description, item string) *jsonSchema {
	str := &jsonSchema{Description: item, Type: typeString, MinLength: 1}
	return &jsonSchema{
		Description: description,
		AnyOf:       []*jsonSchema{str, {Type: typeArray, Items: str}, {Type: typeNull}},
	}
}
