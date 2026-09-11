// Standalone browser preview — renders one insight cell per panel type with
// sample data, no MCP host required. Open dist/preview.html in a browser (or
// `npm run preview`) to see the render surface. Also used for visual testing.

import "./styles.css";
import { renderInto } from "./render.js";
import { mockCell } from "./data.js";
import type { PanelType } from "./schema.js";

const gallery = document.getElementById("gallery")!;
const all: PanelType[] = ["timeseries", "stat", "bar", "table", "logs", "trace", "worklist", "rca", "rulediff", "timeline", "cost", "bullet"];
// #<type> in the URL renders only that panel (handy for focused screenshots).
const only = location.hash.replace("#", "");
const types: PanelType[] = all.includes(only as PanelType) ? [only as PanelType] : all;

for (const panel of types) {
  const card = document.createElement("div");
  card.className = "card";
  gallery.append(card);
  renderInto(card, mockCell({ panel }), (a) => console.log("action", a.kind, a.label));
}

// ?debug forces hover-only affordances visible and expands the query section,
// so a static screenshot can verify the toolbar / dropdown / query views.
if (location.search.includes("debug")) {
  const style = document.createElement("style");
  style.textContent = ".viz-toolbar{opacity:1 !important;transform:none !important;pointer-events:auto !important}";
  document.head.append(style);
  document.querySelectorAll(".qp").forEach((q) => q.classList.add("open"));
  document.querySelectorAll(".qp-toggle").forEach((t) => t.classList.add("open"));
  // force the in-chart tooltip + select-and-ask affordance visible for screenshots
  setTimeout(() => {
    const over = document.querySelector(".u-over") as HTMLElement | null;
    if (over) {
      const r = over.getBoundingClientRect();
      over.dispatchEvent(new MouseEvent("mouseenter", { bubbles: true }));
      over.dispatchEvent(new MouseEvent("mousemove", { clientX: r.left + r.width * 0.55, clientY: r.top + r.height * 0.42, bubbles: true }));
    }
    document.querySelectorAll<HTMLElement>(".ask-btn").forEach((b) => { b.style.display = ""; b.style.left = "58%"; });
  }, 300);
}
