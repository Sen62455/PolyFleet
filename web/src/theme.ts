import type { GlobalThemeOverrides } from "naive-ui";

const fontFamily = 'Inter, "SF Pro Text", "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif';

const componentOverrides: Omit<GlobalThemeOverrides, "common"> = {
  Button: {
    borderRadiusMedium: "5px",
    heightMedium: "36px",
    fontWeight: "600",
  },
  Card: {
    borderRadius: "6px",
  },
  Dialog: {
    borderRadius: "6px",
  },
  Drawer: {
    borderRadius: "0px",
  },
  Input: {
    borderRadius: "5px",
    borderFocus: "1px solid #0f766e",
    boxShadowFocus: "none",
  },
};

export const lightThemeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: "#0f766e",
    primaryColorHover: "#0d9488",
    primaryColorPressed: "#115e59",
    primaryColorSuppl: "#0f766e",
    infoColor: "#2563eb",
    successColor: "#15803d",
    warningColor: "#d97706",
    errorColor: "#e11d48",
    bodyColor: "#f4f7fb",
    cardColor: "#ffffff",
    modalColor: "#ffffff",
    popoverColor: "#ffffff",
    textColorBase: "#0f172a",
    textColor1: "#0f172a",
    textColor2: "#334155",
    textColor3: "#64748b",
    borderColor: "#dbe3ee",
    dividerColor: "#e2e8f0",
    borderRadius: "6px",
    borderRadiusSmall: "4px",
    fontFamily,
  },
  ...componentOverrides,
};

export const darkThemeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: "#2dd4bf",
    primaryColorHover: "#5eead4",
    primaryColorPressed: "#14b8a6",
    primaryColorSuppl: "#2dd4bf",
    infoColor: "#60a5fa",
    successColor: "#34d399",
    warningColor: "#fbbf24",
    errorColor: "#fb7185",
    bodyColor: "#0b1220",
    cardColor: "#111c2f",
    modalColor: "#111c2f",
    popoverColor: "#16243a",
    textColorBase: "#f8fafc",
    textColor1: "#f8fafc",
    textColor2: "#cbd5e1",
    textColor3: "#8fa1b8",
    borderColor: "#26344a",
    dividerColor: "#223149",
    borderRadius: "6px",
    borderRadiusSmall: "4px",
    fontFamily,
  },
  ...componentOverrides,
  Button: {
    ...componentOverrides.Button,
    textColorPrimary: "#042f2e",
    textColorHoverPrimary: "#042f2e",
    textColorPressedPrimary: "#042f2e",
    textColorFocusPrimary: "#042f2e",
    textColorDisabledPrimary: "#5f7777",
  },
  Input: {
    ...componentOverrides.Input,
    borderFocus: "1px solid #2dd4bf",
  },
};

export const eyeThemeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: "#46574f",
    primaryColorHover: "#35463e",
    primaryColorPressed: "#293830",
    primaryColorSuppl: "#52645b",
    infoColor: "#60736a",
    successColor: "#668078",
    warningColor: "#897553",
    errorColor: "#956365",
    bodyColor: "#f3f6f1",
    cardColor: "#fbfcfa",
    modalColor: "#fbfcfa",
    popoverColor: "#fbfcfa",
    textColorBase: "#1c2420",
    textColor1: "#1c2420",
    textColor2: "#4d5a53",
    textColor3: "#718079",
    borderColor: "#dde4da",
    dividerColor: "#dde4da",
    borderRadius: "6px",
    borderRadiusSmall: "4px",
    fontFamily,
  },
  ...componentOverrides,
  Input: {
    ...componentOverrides.Input,
    borderFocus: "1px solid #52645b",
  },
};
