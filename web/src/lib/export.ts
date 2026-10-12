type CSVCell = string | number | boolean | null | undefined;

export function makeCSV(rows: readonly (readonly CSVCell[])[]): string {
  return "\uFEFF" + rows.map((row) => row.map((value) => {
    let text = String(value ?? "").replace(/\0/g, "");
    // Quoting alone does not stop spreadsheet formula evaluation.
    if (typeof value === "string" && (/^[\s\u0001-\u001f]*[=+\-@]/.test(text) || /^[\t\r\n]/.test(text))) {
      text = "'" + text;
    }
    return `"${text.replace(/"/g, '""')}"`;
  }).join(",")).join("\r\n") + "\r\n";
}

export function downloadText(filename: string, content: string, type = "text/csv;charset=utf-8") {
  const url = URL.createObjectURL(new Blob([content], { type }));
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.append(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1_000);
}

export function localDate(value: Date | string | null | undefined): string {
  if (!value) return "";
  const date = value instanceof Date ? value : new Date(value);
  if (!Number.isFinite(date.getTime())) return "";
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
}
