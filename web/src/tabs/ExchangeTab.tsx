export default function ExchangeTab() {
  return (
    <div className="placeholder">
      <span className="phase-chip">Phase 11</span>
      <h2>Not built yet</h2>
      <p>Artificial exchange / economy control. Likely split into sub-phases. The plan:</p>
      <ul className="phase-plan">
        <li>11a — Read-only browse of <code>dune.dune_exchange_orders</code> with filter by template / side / price</li>
        <li>11b — Order management (cancel rogue orders, audit-log every action)</li>
        <li>11c — Bot suite mirroring snapetech: buyer (placement gated by price catalog), seller (validated settlement), populator (seed liquidity), watchdog (cleanup)</li>
        <li>11d — Reviewed catalog editor (template → price band)</li>
      </ul>
      <p className="hint" style={{ marginTop: 12 }}>
        Snapetech's bot suite is currently ~5,400 live seeded orders across 1,176 templates;
        full feature parity is a significantly larger effort than the other phases.
      </p>
    </div>
  )
}
