// Hands a text to the browser as a file download. The object URL is
// revoked after a second, so the text (an export can hold the secret bundle) is
// not reachable through a URL afterwards.
export function saveText(filename: string, text: string, type = "application/yaml"): void {
  const url = URL.createObjectURL(new Blob([text], { type }));
  try {
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    a.rel = "noopener";
    document.body.append(a);
    a.click();
    a.remove();
  } finally {
    // Some browsers cancel the download when the URL goes at once (review nit).
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
}
