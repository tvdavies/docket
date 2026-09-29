import { customElementWidget, type DocketPluginUIV2 } from "@docket/plugin-ui";
import { counters, present, provider } from "./progress";
export const chartPlugin: DocketPluginUIV2 = {
  apiVersion: 2,
  name: "fixture-chart",
  widgets: [
    customElementWidget({
      type: "fixture-chart/samples",
      buildIdentity: "fixture-v1",
      dataVersions: [1],
      present,
      detail: provider,
      hooks: {
        mount(root, context) {
          counters.mounts++;
          counters.active++;
          context.signal.addEventListener("abort", () => counters.aborted++, {
            once: true,
          });
          const style = document.createElement("style");
          style.textContent =
            "svg{display:block;width:100%;height:70px}rect{fill:var(--docket-widget-info-fg)}table{width:100%;border-collapse:collapse}caption{text-align:left}td,th{text-align:left;padding:4px}";
          root.append(style);
          const svg = document.createElementNS(
            "http://www.w3.org/2000/svg",
            "svg",
          );
          svg.setAttribute("viewBox", "0 0 240 70");
          svg.setAttribute("role", "img");
          svg.setAttribute("aria-label", "Fixture sample chart");
          const bars = Array.from({ length: 4 }, (_, i) => {
            const bar = document.createElementNS(svg.namespaceURI, "rect");
            bar.setAttribute("x", String(i * 60 + 5));
            bar.setAttribute("width", "40");
            svg.append(bar);
            return bar;
          });
          const details = document.createElement("details"),
            summary = document.createElement("summary");
          summary.textContent = "View sample values";
          const table = document.createElement("table"),
            caption = document.createElement("caption");
          caption.textContent = "Sample values";
          table.append(caption);
          const cells = Array.from({ length: 4 }, (_, i) => {
            const row = document.createElement("tr"),
              label = document.createElement("th"),
              value = document.createElement("td");
            label.scope = "row";
            label.textContent = `Sample ${i + 1}`;
            row.append(label, value);
            table.append(row);
            return value;
          });
          details.append(summary, table);
          root.append(svg, details);
          const compact = document.createElement("p");
          root.append(compact);
          return {
            update(_snapshot, ctx, view) {
              counters.updates++;
              const count = Number(
                (view.data?.value as { count?: number })?.count || 0,
              );
              svg.style.display = ctx.location === "board" ? "none" : "";
              details.hidden = ctx.location === "board";
              compact.hidden = ctx.location !== "board";
              compact.textContent = `${count} measured samples`;
              for (let i = 0; i < 4; i++) {
                const value = Math.min(60, Math.max(2, (count + i * 7) % 60));
                bars[i].setAttribute("height", String(value));
                bars[i].setAttribute("y", String(65 - value));
                if (cells[i].textContent !== String(value))
                  cells[i].textContent = String(value);
              }
            },
            destroy() {
              counters.active--;
              counters.destroys++;
              root.replaceChildren();
            },
          };
        },
      },
    }),
  ],
};
