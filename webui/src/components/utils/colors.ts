import { system } from "../../../theme";

export const getColors = (semanticTokens: string) => {
  const varColor = system.token(semanticTokens);
  const extractedVar = varColor.match(/var\((--[^)]+)\)/)[1];

  return getComputedStyle(document.documentElement)
    .getPropertyValue(extractedVar)
    .trim();
};
