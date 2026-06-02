package main

// Funcom's component-name hash function, reverse-engineered from
// SUB_0ec82770 in the game-server binary.
//
// Used by dune.actor_inventories.component_name_hash to identify which
// inventory slot on an actor (BackpackInventory, PlayerBankInventory,
// EmoteRadialMenuShortcutInventory, etc.) a given inventory occupies.
//
// Algorithm:
//   - CRC-32 with forward polynomial 0x04C11DB7 (BZIP2/MPEG2 variant —
//     NOT the usual IEEE 0xEDB88320 used by zlib).
//   - Init = 0, final XOR = 0 (no inversion).
//   - Table generated with the left-shift recurrence:
//        T[i] = i << 24
//        for 8 iters: T[i] = (T[i] << 1) ^ POLY  if high bit set else <<1
//     (Standard "forward" / "big-endian" CRC table.)
//   - Input is the UE FString name (TCHAR = UTF-16). ASCII a-z folded to
//     uppercase. Each TCHAR iterated low byte then high byte.
//   - Trailing NUL is skipped (UE FString stores Num = length + 1).

var componentHashTable = func() (t [256]uint32) {
	const poly = uint32(0x04C11DB7)
	for i := 0; i < 256; i++ {
		c := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if c&0x80000000 != 0 {
				c = (c << 1) ^ poly
			} else {
				c <<= 1
			}
		}
		t[i] = c
	}
	return
}()

// componentNameHash returns the int32 hash that Funcom stores in
// actor_inventories.component_name_hash for a given UE subobject name.
// Match the C++ function SUB_0ec82770 exactly: uppercase-fold ASCII a-z,
// iterate each rune as two bytes (low then high) through the CRC table.
func componentNameHash(name string) int32 {
	var h uint32
	for _, r := range name {
		c := uint32(r)
		if c >= 'a' && c <= 'z' {
			c -= 0x20
		}
		lo := c & 0xFF
		hi := (c >> 8) & 0xFF
		h = (h >> 8) ^ componentHashTable[(h^lo)&0xFF]
		h = (h >> 8) ^ componentHashTable[(h^hi)&0xFF]
	}
	return int32(h)
}

// Known subobject names — verified by running componentNameHash() against
// the 17 distinct values currently in the live DB. Each entry was found
// by string-search on the game-server binary for UE member names ending
// in *Inventory or *Component.
var knownComponentNames = map[int32]string{
	-72424899:   "Inventory",
	823000574:   "BackpackInventory",
	1471238483:  "EquipmentInventory",
	-2145738476: "PlayerBankInventory",
	698341964:   "ContractsInventory",
	717356407:   "RadialMenuShortcutInventory",
	-486144023:  "EmoteRadialMenuShortcutInventory",
	1685130861:  "InfluenceInventory",
	-689927216:  "PlayerInboxInventory",
	-1650760960: "DeliveryInventory",
	-243824225:  "TransactionalInventory",
	1169234100:  "P2pTradingInventory",
	-1227602884: "LootInventory",
	-984879668:  "AbilityInventory",
	12480161:    "CraftingComponent",
	367286907:   "PersonalLootContainerInventory",
	-1157873578: "TerminalInventory",
}

// resolveComponentName returns the known UE subobject name for a hash,
// or empty string if not in the table. The lookup is intentionally
// conservative — unknown hashes return "" so the frontend can fall
// back to displaying the raw integer.
func resolveComponentName(hash int32) string {
	return knownComponentNames[hash]
}
