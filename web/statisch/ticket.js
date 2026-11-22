// Vorlage an der Schreibstelle einfügen, gewählte Dateien vorab prüfen.
const text = document.querySelector("textarea#text");
document.querySelector("select[data-vorlagen]")?.addEventListener("change", (e) => {
  const v = e.target.selectedOptions[0]?.dataset.text;
  if (v && text) {
    text.setRangeText(v, text.selectionStart, text.selectionEnd, "end");
    text.focus();
  }
  e.target.selectedIndex = 0;
});
document.querySelectorAll("input[type=file][data-max]").forEach((f) =>
  f.addEventListener("change", () => {
    const zuGross = [...f.files].some((d) => d.size > Number(f.dataset.max));
    f.setCustomValidity(f.files.length > 5 ? "Höchstens 5 Dateien." : zuGross ? "Höchstens 10 MB je Datei." : "");
    f.reportValidity();
  }));
