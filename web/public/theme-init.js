(function () {
  var preference = "system";
  var mode = "light";
  var systemDark = false;

  try {
    systemDark = typeof window.matchMedia === "function"
      && window.matchMedia("(prefers-color-scheme: dark)").matches;
  } catch (_error) {
    systemDark = false;
  }

  try {
    var stored = window.localStorage.getItem("hyfleet:color-mode");
    preference = stored === "system" || stored === "light" || stored === "dark" || stored === "eye"
      ? stored
      : "system";
  } catch (_error) {
    preference = "system";
  }

  mode = preference === "system"
    ? systemDark ? "dark" : "light"
    : preference;

  document.documentElement.dataset.theme = mode;
  document.documentElement.dataset.themePreference = preference;
  document.documentElement.style.colorScheme = mode === "dark" ? "dark" : "light";

  var colorScheme = document.querySelector('meta[name="color-scheme"]');
  var themeColor = document.querySelector('meta[name="theme-color"]');
  if (colorScheme) colorScheme.setAttribute("content", mode === "dark" ? "dark" : "light");
  if (themeColor) {
    themeColor.setAttribute("content", mode === "dark" ? "#0B0F17" : mode === "eye" ? "#F2EFE6" : "#F3F5F8");
  }
})();
