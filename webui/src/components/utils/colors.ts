import { system } from "../../../theme";

const category20 = [
  "#1f77b4",
  "#aec7e8",
  "#ff7f0e",
  "#ffbb78",
  "#2ca02c",
  "#98df8a",
  "#d62728",
  "#ff9896",
  "#9467bd",
  "#c5b0d5",
  "#8c564b",
  "#c49c94",
  "#e377c2",
  "#f7b6d2",
  "#7f7f7f",
  "#c7c7c7",
  "#bcbd22",
  "#dbdb8d",
  "#17becf",
  "#9edae5",
];

export const chartColorMap = (idx: number) => {
  return category20[idx % category20.length];
};

export const getColors = (semanticTokens: string) => {
  const varColor = system.token(semanticTokens);
  const extractedVar = varColor.match(/var\((--[^)]+)\)/)[1];

  return getComputedStyle(document.documentElement)
    .getPropertyValue(extractedVar)
    .trim();
};
