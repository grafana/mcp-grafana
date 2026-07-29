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

// Reject non-http(s) schemes (e.g. javascript:, data:) before linking.
const safeHttpUrl = (raw: string): string | null => {
  try {
    const parsed = new URL(raw);
    if (parsed.protocol === "http:" || parsed.protocol === "https:") {
      return parsed.toString();
    }
  } catch {
    // Not a parseable absolute URL.
  }
  return null;
};

app.ontoolresult = (result: any) => {
  const content = result?.content;
  if (!content) return;

  containerEl.innerHTML = "";
  deeplinkEl.innerHTML = "";
  deeplinkEl.style.display = "none";

  if (result.isError) {
    const message = content
      .filter(
        (item: any) =>
          item?.type === "text" &&
          typeof item.text === "string" &&
          !isDeeplinkItem(item)
      )
      .map((item: any) => item.text)
      .join("\n")
      .trim();
    statusEl.textContent = message || "Failed to render panel.";
    statusEl.style.display = "block";
    return;
  }

  statusEl.style.display = "none";

  for (const item of content) {
    if (item.type === "image" && item.data) {
      const img = document.createElement("img");
      img.src = `data:${item.mimeType || "image/png"};base64,${item.data}`;
      img.alt = "Grafana panel";
      containerEl.appendChild(img);
    }
    if (isDeeplinkItem(item)) {
      const url = safeHttpUrl(item.text as string);
      if (!url) continue;
      const a = document.createElement("a");
      a.href = url;
      a.textContent = "Open in Grafana";
      a.addEventListener("click", (e) => {
        e.preventDefault();
        app.openLink({ url });
      });
      deeplinkEl.appendChild(a);
      deeplinkEl.style.display = "block";
    }
  }
};

app.connect();
