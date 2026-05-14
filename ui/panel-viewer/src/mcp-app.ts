import { App } from "@modelcontextprotocol/ext-apps";

const statusEl = document.getElementById("status")!;
const containerEl = document.getElementById("image-container")!;

const app = new App(
  { name: "Grafana Panel Viewer", version: "1.0.0" },
  {},
  { autoResize: true }
);

app.ontoolresult = (result: any) => {
  const content = result?.content;
  if (!content) return;

  statusEl.style.display = "none";

  for (const item of content) {
    if (item.type === "image" && item.data) {
      containerEl.innerHTML = "";
      const img = document.createElement("img");
      img.src = `data:${item.mimeType || "image/png"};base64,${item.data}`;
      img.alt = "Grafana panel";
      containerEl.appendChild(img);
    }
  }
};

app.connect();
