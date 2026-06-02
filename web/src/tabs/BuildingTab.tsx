export default function BuildingTab() {
  return (
    <div className="placeholder">
      <span className="phase-chip">Phase 9</span>
      <h2>Not built yet</h2>
      <p>Player-built content management — vehicles, bases, blueprints. The plan:</p>
      <ul className="phase-plan">
        <li>Vehicle backup browse + restore (<code>dune.backup_vehicles</code>, <code>dune.recovered_vehicles</code>, <code>dune.load_backup_vehicle</code>)</li>
        <li>Base backup browse + restore (<code>dune.base_backups</code>, <code>dune.base_backup_get_available_backups</code>)</li>
        <li>Blueprint export/import — copy a player's blueprint to another character or a JSON file</li>
        <li>Search by owner / template / created-after timestamp</li>
      </ul>
    </div>
  )
}
