import { h, state, apiCall, formatDate } from "./state.js";

export async function loadDashboardSystemMetrics() {
  // Host system CPU/Mem monitoring removed
}


export function loadDashboard() {
  loadDashboardSystemMetrics();
  document.getElementById("dash-models-count").textContent = state.models.filter(m => !m.hidden).length;
  document.getElementById("dash-profiles-count").textContent = state.profiles.length;
  
  const active = state.servers.filter(s => s.status === "healthy" || s.status === "starting");
  const crashed = state.servers.filter(s => s.status === "crashed");
  
  document.getElementById("dash-active-count").textContent = active.length;
  document.getElementById("dash-crashed-count").textContent = crashed.length;
  document.getElementById("active-servers-badge").textContent = `${active.length} Active`;

  // Render running servers
  const serversList = document.getElementById("dash-servers-list");
  if (active.length === 0 && crashed.length === 0) {
    serversList.innerHTML = `<div class="empty-state">No servers currently active. Go to Profile Builder to start one!</div>`;
  } else {
    const allDisplay = [...active, ...crashed];
    serversList.replaceChildren(
      ...allDisplay.map(srv => {
        const prof = state.profiles.find(p => p.id === srv.profile_id);
        const name = prof ? prof.name : "Unknown Profile";
        const isMultimodal = prof && Array.isArray(prof.args) && prof.args.some((a, idx) => a === "--mmproj" && idx + 1 < prof.args.length && prof.args[idx+1] !== "");
        const statusClass = srv.status === "healthy" ? "green" : (srv.status === "crashed" ? "red" : "yellow");
        const card = h("div", {
          class: "stat-card",
          style: "border: 1px solid rgba(255,255,255,0.05); margin-bottom: 8px; cursor: pointer;"
        },
          h("div", {style: "display:flex; justify-content:space-between; align-items:center;"},
            h("div", {},
              h("strong", {style: "display:flex; align-items:center; gap:8px; font-size:0.95rem;"}, 
                name,
                isMultimodal ? h("span", {style: "font-size: 0.65rem; background: rgba(147, 51, 234, 0.15); color: #c084fc; border: 1px solid rgba(147, 51, 234, 0.3); padding: 1px 5px; border-radius: 4px; font-weight: normal;"}, "✨ Multimodal") : ""
              ),
              h("span",   {style: "font-size:0.75rem; color:var(--text-dim);"}, `PID: ${srv.pid} | Port: ${srv.port}`)
            ),
            h("span", {class: `status-pill ${statusClass}`}, srv.status)
          )
        );
        card.addEventListener("click", () => {
          state.activeProfileIdInLifecycle = srv.profile_id;
          document.querySelector("[data-target=servers]").click();
          if (window.openProfileDetail) {
            setTimeout(() => window.openProfileDetail(srv.profile_id), 50);
          }
        });
        return card;
      })
    );
  }

  // Render recent benchmarks
  loadRecentBenchmarksDashboard();
}

export async function loadRecentBenchmarksDashboard() {
  const dashBenchList = document.getElementById("dash-bench-list");
  try {
    state.benchmarks = await apiCall("/api/benchmarks");
    
    // Sort benchmarks by started_at descending (newest first)
    const completed = state.benchmarks
      .filter(b => b.status === "completed")
      .sort((a, b) => new Date(b.started_at) - new Date(a.started_at))
      .slice(0, 4);
    
    if (completed.length === 0) {
      dashBenchList.innerHTML = `<div class="empty-state">No benchmarks completed yet. Go to Server Lifecycle to trigger one!</div>`;
    } else {
      dashBenchList.replaceChildren(
        ...completed.map(b => {
          const prof = state.profiles.find(p => p.id === b.profile_id);
          const name = prof ? prof.name : "Profile";
          
          let speedVal = 0;
          let latencyStr = "";
          if (b.cells && b.cells.length > 0) {
            const first = b.cells[0];
            speedVal = parseFloat(first.aggregates.tg_speed_mean || 0);
            const lat = parseFloat(first.aggregates.e2e_p50 || 0);
            if (lat > 0) latencyStr = ` · Latency: ${lat.toFixed(0)}ms`;
          } else if (b.result) {
            speedVal = parseFloat(b.result.avg_tokens_per_sec || 0);
            const lat = parseFloat(b.result.avg_latency_ms || 0);
            if (lat > 0) latencyStr = ` · Latency: ${lat.toFixed(0)}ms`;
          }
          const speed = speedVal > 0 ? speedVal.toFixed(1) : "0";

          let typeLabel = "Single-Shot";
          if (b.kind === "sweep") typeLabel = `Sweep (${b.sweep_flag})`;
          if (b.kind === "concurrency") typeLabel = "Concurrency";

          const workload = b.workload_id || "custom";

          return h("div", {style: "padding:12px 14px; border-bottom: 1px solid rgba(255,255,255,0.03); display:flex; justify-content:space-between; align-items:center;"},
            h("div", {},
              h("div", {style: "display:flex; align-items:center; gap:8px; margin-bottom:3px;"},
                h("strong", {style: "font-size:0.92rem;"}, name),
                h("span", {class: "status-pill yellow", style: "font-size:0.68rem; padding:2px 6px;"}, typeLabel)
              ),
              h("div", {style: "font-size:0.72rem; color:var(--text-dim);"}, 
                formatDate(b.started_at), 
                h("span", {style: "opacity:0.65;"}, ` · Workload: ${workload}${latencyStr}`)
              )
            ),
            h("span", {style: "font-size:1.05rem; font-weight:800; color:var(--accent-pink); white-space:nowrap;"}, `${speed} T/s`)
          );
        })
      );
    }
  } catch (err) {
    console.error(err);
    dashBenchList.innerHTML = `<div class="empty-state">Failed to load benchmarks.</div>`;
  }
}
