import { h, state, apiCall, formatDate } from "./state.js";

export async function loadDashboardSystemMetrics() {
  try {
    const metrics = await apiCall("/api/system/metrics");
    const cpuVal = document.getElementById("dash-instances-cpu");
    const memVal = document.getElementById("dash-instances-mem");
    if (cpuVal) {
      cpuVal.textContent = parseFloat(metrics.instances_cpu_sum || 0).toFixed(1) + "%";
    }
    if (memVal) {
      memVal.textContent = (parseFloat(metrics.instances_mem_sum || 0) / 1024 / 1024 / 1024).toFixed(2) + " GB";
    }
  } catch (err) {
    console.error("Failed to load dashboard system metrics", err);
  }
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
        const statusClass = srv.status === "healthy" ? "green" : (srv.status === "crashed" ? "red" : "yellow");
        const card = h("div", {
          class: "stat-card",
          style: "border: 1px solid rgba(255,255,255,0.05); margin-bottom: 8px; cursor: pointer;"
        },
          h("div", {style: "display:flex; justify-content:space-between; align-items:center;"},
            h("div", {},
              h("strong", {style: "display:block; font-size:0.95rem;"}, name),
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
    const completed = state.benchmarks.filter(b => b.status === "completed").slice(-4).reverse();
    
    if (completed.length === 0) {
      dashBenchList.innerHTML = `<div class="empty-state">No benchmarks completed yet. Go to Server Lifecycle to trigger one!</div>`;
    } else {
      dashBenchList.replaceChildren(
        ...completed.map(b => {
          const prof = state.profiles.find(p => p.id === b.profile_id);
          const name = prof ? prof.name : "Profile";
          const speed = b.result && b.result.avg_tokens_per_sec ? parseFloat(b.result.avg_tokens_per_sec).toFixed(2) : "0";
          return h("div", {style: "padding:12px; border-bottom: 1px solid rgba(255,255,255,0.03); display:flex; justify-content:space-between; align-items:center;"},
            h("div", {},
              h("strong", {style: "display:block; font-size:0.9rem;"}, name),
              h("span",   {style: "font-size:0.7rem; color:var(--text-dim);"}, formatDate(b.started_at))
            ),
            h("span", {style: "font-size:1.1rem; font-weight:800; color:var(--accent-pink);"}, `${speed} T/s`)
          );
        })
      );
    }
  } catch {
    dashBenchList.innerHTML = `<div class="empty-state">Failed to load benchmarks.</div>`;
  }
}
