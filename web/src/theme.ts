import type { GlobalThemeOverrides } from "naive-ui";

const fontFamily = "'Inter Variable', Inter, 'PingFang SC', 'HarmonyOS Sans SC', 'Microsoft YaHei UI', 'Microsoft YaHei', system-ui, sans-serif";
const fontFamilyMono = "'JetBrains Mono Variable', 'Cascadia Mono', 'SF Mono', Consolas, monospace";

// 每套主题只需要填这一张表；色值与 refresh.css 里的 --hf-* 一一对应
interface Palette {
  primary: string; primaryHover: string; primaryPressed: string; onPrimary: string;
  success: string; successHover: string; warning: string; warningHover: string; error: string; errorHover: string;
  successFill: string; warningFill: string; errorFill: string; // 进度条填充用“亮一档”的状态色
  body: string; card: string; raised: string; inputBg: string; hover: string; subtle: string;
  text1: string; text2: string; text3: string; placeholder: string;
  border: string; divider: string; ring: string;
  shadow2: string; shadow3: string;
}

function build(p: Palette): GlobalThemeOverrides {
  return {
    common: {
      primaryColor: p.primary,
      primaryColorHover: p.primaryHover,
      primaryColorPressed: p.primaryPressed,
      primaryColorSuppl: p.primary,
      infoColor: p.primary,
      infoColorHover: p.primaryHover,
      infoColorPressed: p.primaryPressed,
      infoColorSuppl: p.primary,
      successColor: p.success,
      successColorHover: p.successHover,
      successColorPressed: p.success,
      successColorSuppl: p.success,
      warningColor: p.warning,
      warningColorHover: p.warningHover,
      warningColorPressed: p.warning,
      warningColorSuppl: p.warning,
      errorColor: p.error,
      errorColorHover: p.errorHover,
      errorColorPressed: p.error,
      errorColorSuppl: p.error,
      bodyColor: p.body,
      cardColor: p.card,
      modalColor: p.raised,
      popoverColor: p.raised,
      inputColor: p.inputBg,
      actionColor: p.subtle,
      hoverColor: p.hover,
      tableHeaderColor: p.subtle,
      textColorBase: p.text1,
      textColor1: p.text1,
      textColor2: p.text2,
      textColor3: p.text3,
      placeholderColor: p.placeholder,
      borderColor: p.border,
      dividerColor: p.divider,
      borderRadius: "10px",
      borderRadiusSmall: "8px",
      boxShadow2: p.shadow2,
      boxShadow3: p.shadow3,
      fontFamily,
      fontFamilyMono,
    },
    Button: {
      borderRadiusTiny: "8px",
      borderRadiusSmall: "8px",
      borderRadiusMedium: "10px",
      borderRadiusLarge: "10px",
      heightSmall: "30px",
      heightMedium: "36px",
      fontWeight: "500",
      textColorPrimary: p.onPrimary,
      textColorHoverPrimary: p.onPrimary,
      textColorPressedPrimary: p.onPrimary,
      textColorFocusPrimary: p.onPrimary,
      textColorDisabledPrimary: p.onPrimary,
    },
    Input: {
      borderRadius: "10px",
      heightMedium: "36px",
      borderHover: `1px solid ${p.primary}`,
      borderFocus: `1px solid ${p.primary}`,
      boxShadowFocus: `0 0 0 3px ${p.ring}`,
    },
    InternalSelection: {
      borderRadius: "10px",
      heightMedium: "36px",
      borderHover: `1px solid ${p.primary}`,
      borderActive: `1px solid ${p.primary}`,
      borderFocus: `1px solid ${p.primary}`,
      boxShadowActive: `0 0 0 3px ${p.ring}`,
      boxShadowFocus: `0 0 0 3px ${p.ring}`,
    },
    Tag: {
      borderRadius: "999px",
    },
    Card: {
      borderRadius: "14px",
    },
    Dialog: {
      borderRadius: "14px",
    },
    Drawer: {
      borderRadius: "0px",
      titleFontSize: "16px",
      titleFontWeight: "600",
    },
    Progress: {
      railHeight: "6px",
      fillColor: p.primary,
      fillColorInfo: p.primary,
      fillColorSuccess: p.successFill,
      fillColorWarning: p.warningFill,
      fillColorError: p.errorFill,
    },
  };
}

export const lightThemeOverrides = build({
  primary: "#3451D1", primaryHover: "#2A41B0", primaryPressed: "#223596", onPrimary: "#FFFFFF",
  success: "#087052", successHover: "#0A7B5C", warning: "#8F5400", warningHover: "#A35F00", error: "#AD2D1A", errorHover: "#BF3320",
  successFill: "#12A37E", warningFill: "#CC7A00", errorFill: "#DC4A32",
  body: "#F3F5F8", card: "#FFFFFF", raised: "#FFFFFF", inputBg: "#FFFFFF", hover: "#F1F4F9", subtle: "#F8FAFC",
  text1: "#111A2E", text2: "#364258", text3: "#5E6A81", placeholder: "#7F8BA1",
  border: "#D3D9E3", divider: "#E4E8EF", ring: "rgba(52, 81, 209, 0.22)",
  shadow2: "0 10px 28px -10px rgba(17, 26, 46, 0.14)", shadow3: "0 20px 48px -16px rgba(17, 26, 46, 0.22)",
});

export const darkThemeOverrides = build({
  primary: "#7B93FF", primaryHover: "#98AAFF", primaryPressed: "#6277E6", onPrimary: "#0A1030",
  success: "#5EE0B6", successHover: "#34D3A0", warning: "#F5BF6B", warningHover: "#F2B14C", error: "#FF8F7D", errorHover: "#FF7A66",
  successFill: "#34D3A0", warningFill: "#F2B14C", errorFill: "#FF7A66",
  body: "#0B0F17", card: "#121826", raised: "#182033", inputBg: "#0F1521", hover: "#182033", subtle: "#0F1521",
  text1: "#E9EEF8", text2: "#BAC4D6", text3: "#8C98AF", placeholder: "#5F6C85",
  border: "#31405A", divider: "#232D40", ring: "rgba(123, 147, 255, 0.35)",
  shadow2: "0 14px 32px -12px rgba(0, 0, 0, 0.6)", shadow3: "0 24px 56px -16px rgba(0, 0, 0, 0.7)",
});

export const eyeThemeOverrides = build({
  primary: "#3C5A8C", primaryHover: "#2F4A75", primaryPressed: "#253B5E", onPrimary: "#FFFFFF",
  success: "#35694E", successHover: "#4E8A6B", warning: "#8A5A0C", warningHover: "#B07A1E", error: "#983E2E", errorHover: "#B5503E",
  successFill: "#4E8A6B", warningFill: "#B07A1E", errorFill: "#B5503E",
  body: "#F2EFE6", card: "#FAF8F2", raised: "#FAF8F2", inputBg: "#FAF8F2", hover: "#EEEADD", subtle: "#F0EDE2",
  text1: "#25231D", text2: "#4A463B", text3: "#6B6655", placeholder: "#8A846F",
  border: "#D2CBB8", divider: "#E2DDCF", ring: "rgba(60, 90, 140, 0.22)",
  shadow2: "0 10px 24px -10px rgba(60, 50, 30, 0.16)", shadow3: "0 20px 44px -16px rgba(60, 50, 30, 0.24)",
});
