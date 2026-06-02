export default function MapTab() {
  return (
    <div className="placeholder">
      <span className="phase-chip">Phase 10</span>
      <h2>Not built yet</h2>
      <p>Visual map views. The plan:</p>
      <ul className="phase-plan">
        <li>Hagga Basin render — use snapetech's <code>hagga-basin.webp</code> + <code>hagga-pois.json</code> as the base layer</li>
        <li>Overlay player markers (<code>dune.markers</code>, <code>dune.player_markers</code>) with click-through to detail</li>
        <li>Deep Desert tab: hotspot overlay + per-cycle shifting-sands status from <code>dune.shiftingsands_data</code></li>
        <li>Toggle layers (POIs / markers / resource fields / player respawn locations)</li>
      </ul>
    </div>
  )
}
