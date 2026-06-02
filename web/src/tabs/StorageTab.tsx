export default function StorageTab() {
  return (
    <div className="placeholder">
      <span className="phase-chip">Phase 8</span>
      <h2>Not built yet</h2>
      <p>
        Server-side storage containers — Spicefield depots, exchanges, and caches. The plan:
      </p>
      <ul className="phase-plan">
        <li>List <code>dune.inventories</code> filtered to non-actor types (storage/exchange/cache/depot)</li>
        <li>Click a container to see its items + capacity</li>
        <li>Give-item form similar to the Players inventory editor</li>
        <li>Bulk transfer between containers (later)</li>
      </ul>
    </div>
  )
}
