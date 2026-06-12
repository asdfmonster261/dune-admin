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
	Name       string   `json:"name,omitempty"`
	Categories []string `json:"categories"`
}

// What we expose over HTTP — each row has the template id + optional
// display name. Wire shape lets the React side render "Display (id)" in
// the datalist while submitting the raw id back to the backend.
type ItemListRow struct {
	Id   string `json:"id"`
	Name string `json:"name,omitempty"`
}

var (
	itemsCatalog       map[string]ItemCatalogEntry
	itemsCatalogList   []ItemListRow   // sorted by id
)

func init() {
	if err := json.Unmarshal(itemsCatalogJSON, &itemsCatalog); err != nil {
		panic("items.json embed: " + err.Error())
	}
	ids := make([]string, 0, len(itemsCatalog))
	for k := range itemsCatalog {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	itemsCatalogList = make([]ItemListRow, len(ids))
	for i, id := range ids {
		itemsCatalogList[i] = ItemListRow{Id: id, Name: itemsCatalog[id].Name}
	}
}
