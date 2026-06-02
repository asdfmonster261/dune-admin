package main

import "testing"

// Verify the reverse-engineered hash function against all 17 distinct
// hashes observed in the live DB. The mapping was bootstrapped by
// brute-forcing candidate UE subobject names from the game-server
// binary's string table until every observed hash had a preimage.
func TestComponentNameHash(t *testing.T) {
	cases := map[string]int32{
		"Inventory":                        -72424899,
		"BackpackInventory":                823000574,
		"EquipmentInventory":               1471238483,
		"PlayerBankInventory":              -2145738476,
		"ContractsInventory":               698341964,
		"RadialMenuShortcutInventory":      717356407,
		"EmoteRadialMenuShortcutInventory": -486144023,
		"InfluenceInventory":               1685130861,
		"PlayerInboxInventory":             -689927216,
		"DeliveryInventory":                -1650760960,
		"TransactionalInventory":           -243824225,
		"P2pTradingInventory":              1169234100,
		"LootInventory":                    -1227602884,
		"AbilityInventory":                 -984879668,
		"CraftingComponent":                12480161,
		"PersonalLootContainerInventory":   367286907,
		"TerminalInventory":                -1157873578,
	}
	for name, expected := range cases {
		got := componentNameHash(name)
		if got != expected {
			t.Errorf("componentNameHash(%q) = %d, want %d", name, got, expected)
		}
	}
}

func TestResolveComponentName(t *testing.T) {
	if got := resolveComponentName(823000574); got != "BackpackInventory" {
		t.Errorf("resolveComponentName(823000574) = %q, want BackpackInventory", got)
	}
	if got := resolveComponentName(int32(int64(0xDEADBEEF) - (1 << 32))); got != "" {
		t.Errorf("resolveComponentName(unknown) = %q, want empty", got)
	}
}
