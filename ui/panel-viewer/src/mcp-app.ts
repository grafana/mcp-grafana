import { App } from "@modelcontextprotocol/ext-apps";

const statusEl = document.getElementById("status")!;
const containerEl = document.getElementById("image-container")!;
const deeplinkEl = document.getElementById("deeplink")!;

const app = new App(
  { name: "Grafana Panel Viewer", version: "1.0.0" },
  {},
  { autoResize: true }
);

// Mirrors UIContentKindDeeplink in ui_apps.go.
const DEEPLINK_KIND = "deeplink";

const isDeeplinkItem = (item: any): boolean =>
  item?.type === "text" &&
  typeof item.text === "string" &&
  item._meta?.ui?.kind === DEEPLINK_KIND;

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
    if (isDeeplinkItem(item)) {
      const url = item.text as string;
      const a = document.createElement("a");
      a.href = url;
      a.textContent = "Open in Grafana";
      a.addEventListener("click", (e) => {
        e.preventDefault();
        app.openLink({ url });
      });
      deeplinkEl.innerHTML = "";
      deeplinkEl.appendChild(a);
      deeplinkEl.style.display = "block";
    }
  }
};

app.connect();
