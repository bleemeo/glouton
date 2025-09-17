/* eslint-disable @typescript-eslint/no-explicit-any */
import * as d3 from "d3";
import { isNullOrUndefined } from ".";

let dayFormater: (arg0: Date) => string,
  monthFormater: (arg0: Date) => string,
  yearFormater: (arg0: Date) => string,
  hoursMinutesFormater: (arg0: Date) => string;

// some browser (FF mobile) doesn't support yet the Intl API
if (window.Intl) {
  // 2 digits day of month
  dayFormater = new Intl.DateTimeFormat("en", {
    year: undefined,
    month: undefined,
    day: "2-digit",
  }).format;
  // the month in English on 3 letters: Jan, Feb...
  monthFormater = new Intl.DateTimeFormat("en", {
    year: undefined,
    month: "short",
    day: undefined,
  }).format;
  // 2 digits year
  yearFormater = new Intl.DateTimeFormat("en", {
    year: "2-digit",
    month: undefined,
    day: undefined,
  }).format;
  // hours & minutes follow the locale (17:05 for Europe, 5:05 PM for US)
  hoursMinutesFormater = new Intl.DateTimeFormat(
    window.navigator.language || "en",
    {
      year: undefined,
      month: undefined,
      day: undefined,
      hour: "2-digit",
      minute: "2-digit",
    },
  ).format;
} else {
  dayFormater = d3.timeFormat("%d");
  monthFormater = d3.timeFormat("%b");
}

export const formatDateTime = (date: string | number | Date | undefined) => {
  if (!date) {
    return "";
  }
  const d = date instanceof Date ? date : new Date(date);

  const day = dayFormater(d);
  // the month in English on 3 letters: Jan, Feb...
  const month = monthFormater(d);
  // 2 digits year
  const year = yearFormater(d);
  // hours & minutes follow the locale (17:05 for Europe, 5:05 PM for US)
  const hoursMinutes = hoursMinutesFormater(d);

  return `${day}/${month}/${year} ${hoursMinutes}`;
};

export const formatDateTimeWithSeconds = (date: string | number | Date) => {
  const d = date instanceof Date ? date : new Date(date);
  const day = dayFormater(d);
  const month = monthFormater(d);
  const year = yearFormater(d);
  const seconds = d.getSeconds();
  const minutes = d.getMinutes();
  const hours = d.getHours();
  const time =
    (hours < 10 ? "0" + hours : hours) +
    ":" +
    (minutes < 10 ? "0" + minutes : minutes) +
    ":" +
    (seconds < 10 ? "0" + seconds : seconds);
  return `${day}/${month}/${year} ${time}`;
};

const d3FormaterHandlingNull = (formatter: string, suffix = "") => {
  const fmt = d3.format(formatter);
  return (value: any) =>
    isNullOrUndefined(value) ? "N/A" : fmt(value) + suffix;
};

export const bytesToString = function (bytes: number) {
  const fmt = d3FormaterHandlingNull(".3r");
  const units = ["B", "kB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"];

  for (let idx = 0; idx < units.length; idx++) {
    if (bytes < Math.pow(1024, idx + 1)) {
      return fmt(bytes / Math.pow(1024, idx)) + units[idx];
    }
  }
};

export const formatToBytes = function (bytes: number) {
  const fmt = d3FormaterHandlingNull(".3r");
  const units = ["B", "kB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"];

  for (let idx = 0; idx < units.length; idx++) {
    if (bytes < Math.pow(1024, idx + 1)) {
      return [fmt(bytes / Math.pow(1024, idx)), units[idx]];
    }
  }
};

export const formatToBits = (bits: number) => {
  const fmt = d3FormaterHandlingNull(".3r");
  const units = ["", "k", "M", "G", "T", "P", "E", "Z", "Y"];

  for (let idx = 0; idx < units.length; idx++) {
    if (bits < Math.pow(1000, idx + 1)) {
      return [fmt(bits / Math.pow(1000, idx)), units[idx] + "bit"];
    }
  }
};

export const percentToString = function (percent: number) {
  const formatter =
    Math.trunc(percent) > 0
      ? d3FormaterHandlingNull(".3r")
      : d3FormaterHandlingNull(".2g");
  return formatter(percent) + " %";
};

export const percentToString2Digits = (percent: number) => {
  const formatter =
    Math.trunc(percent) > 0
      ? d3FormaterHandlingNull(".2r")
      : d3FormaterHandlingNull(".1f");
  return formatter(percent) + " %";
};

export const defaultToString = function (value?: number) {
  return value ? "N/A" : d3.format(".3")(value!);
};

export const twoDigitsWithMetricPrefix = (value: number) => {
  const fmt = d3FormaterHandlingNull(".2f");
  const units = ["", "k", "M", "G", "T", "P", "E", "Z", "Y"];

  for (let idx = 0; idx < units.length; idx++) {
    if (value < Math.pow(1000, idx + 1)) {
      return fmt(value / Math.pow(1000, idx)) + units[idx];
    }
  }
};

export const intUnit = (value: number) => {
  return Math.round(value).toString();
};

export const unitFormatCallback = function (
  unit?: number,
): (arg0?: number) => string | string[] | undefined {
  // UNIT_FLOAT = 0;
  // UNIT_PERCENTAGE = 1;
  // UNIT_INT = 2;
  switch (unit) {
    case 0:
      return twoDigitsWithMetricPrefix;
    case 1:
      return percentToString;
    case 2:
      return intUnit;
    default:
      return defaultToString;
  }
};

export function _formatCpuTime(time: number) {
  const minutes = Math.trunc(time / 60);
  return `${minutes}:${d3.format(".2f")(time % 60)}`;
}

export const getHoursFromDateString = (dateString: string) => {
  return new Date(dateString).toTimeString().split(" ")[0];
};
