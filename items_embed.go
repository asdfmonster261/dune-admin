package main

import (
	_ "embed"
	"encoding/json"
	"sort"
)

// Item-template catalog regenerated from server paks via
// /workspace/dune-pak-tools/dump_templates_v2.py. Re-embed by copying
// the regenerated items.json over /workspace/dune-admin/items.json
// then `go build`.
//
// Shape:
//   { "<TemplateId>": { "categories": ["<DT_BaseItems_*_suffix>", ...] } }
//
// Used by AddItemToInventory's ItemName autocomplete in the
// AdminActions tab.

//go:embed items.json
var itemsCatalogJSON []byte

type ItemCatalogEntry struct {
	Categories []string `json:"categories"`
}

var (
	itemsCatalog       map[string]ItemCatalogEntry
	itemsCatalogList   []string   // sorted template ids
)

func init() {
	if err := json.Unmarshal(itemsCatalogJSON, &itemsCatalog); err != nil {
		panic("items.json embed: " + err.Error())
	}
	itemsCatalogList = make([]string, 0, len(itemsCatalog))
	for k := range itemsCatalog {
		itemsCatalogList = append(itemsCatalogList, k)
	}
	sort.Strings(itemsCatalogList)
}
