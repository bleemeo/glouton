/* eslint-disable @typescript-eslint/no-explicit-any */
import * as d3 from "d3";
import { isNullOrUndefined } from ".";

let dayFormater: (arg0: Date) => string,
  monthFormater: (arg0: Date) => string,
  yearFormater: (arg0: Date) => string,
  hoursMinutesFormater: (arg0: Date) => string,
  hoursMinutesSecondsFormater: (arg0: Date) => string,
  monthFormater2Digit: (arg0: Date) => string,
  yearFormater4Digit: (arg0: Date) => string,
  fullMonthFormater: (arg0: Date) => string;

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
  // The fullname month in English
  fullMonthFormater = new Intl.DateTimeFormat("en", {
    year: undefined,
    month: "long",
    day: undefined,
  }).format;
  // 2 digits year
  yearFormater = new Intl.DateTimeFormat("en", {
    year: "2-digit",
    month: undefined,
    day: undefined,
  }).format;
  // 2 digit month
  monthFormater2Digit = new Intl.DateTimeFormat("en", {
    year: undefined,
    month: "2-digit",
    day: undefined,
  }).format;
  // 4 digits year
  yearFormater4Digit = new Intl.DateTimeFormat("en", {
    year: "numeric",
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
  hoursMinutesSecondsFormater = new Intl.DateTimeFormat(
    window.navigator.language || "en",
    {
      year: undefined,
      month: undefined,
      day: undefined,
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    },
  ).format;
} else {
  dayFormater = d3.timeFormat("%d");
  monthFormater = d3.timeFormat("%b");
  monthFormater2Digit = d3.timeFormat("%m");
  fullMonthFormater = d3.timeFormat("%B");
  yearFormater = d3.timeFormat("%y");
  yearFormater4Digit = d3.timeFormat("%Y");
  hoursMinutesFormater = d3.timeFormat("%I:%M%p");
  hoursMinutesSecondsFormater = d3.timeFormat("%I:%M:%S%p");
}

export const formatDate = (date: string | number | Date) => {
  const d = date instanceof Date ? date : new Date(date);

  const day = dayFormater(d);
  // the month in English on 3 letters: Jan, Feb...
  const month = monthFormater(d);
  // 2 digits year
  const year = yearFormater(d);

  return `${day}/${month}/${year}`;
};

export const formatDateFullYear = (date: string | number | Date) => {
  const d = date instanceof Date ? date : new Date(date);

  const day = dayFormater(d);
  // the month in English on 3 letters: Jan, Feb...
  const month = monthFormater(d);
  // 2 digits year
  const year = yearFormater4Digit(d);

  return `${day}/${month}/${year}`;
};

const convertToUTC = (d: Date) => {
  return new Date(d.getTime() + d.getTimezoneOffset() * 60000);
};

export const formathYearMonth = (date: string | number | Date) => {
  const d = date instanceof Date ? date : new Date(date);
  const utc = convertToUTC(d);
  const month = monthFormater2Digit(utc);
  const year = yearFormater4Digit(utc);
  return `${month}/${year}`;
};

export const formatToFullMonthYear = (date: string | number | Date) => {
  const d = date instanceof Date ? date : new Date(date);
  const utc = convertToUTC(d);
  const month = fullMonthFormater(utc);
  const year = yearFormater4Digit(utc);
  return `${month} ${year}`;
};

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

export const formatToFrenchTime = (date: string | number | Date) => {
  const d = date instanceof Date ? date : new Date(date);
  const seconds = d.getSeconds();
  const minutes = d.getMinutes();
  const hours = d.getHours();
  return (
    (hours < 10 ? "0" + hours : hours) +
    ":" +
    (minutes < 10 ? "0" + minutes : minutes) +
    ":" +
    (seconds < 10 ? "0" + seconds : seconds)
  );
};

export const formatDateWithSecond = (date: string | number | Date) => {
  const d = date instanceof Date ? date : new Date(date);

  const day = dayFormater(d);
  // the month in English on 3 letters: Jan, Feb...
  const month = monthFormater(d);
  // 2 digits year
  const year = yearFormater(d);
  // hours & minutes & seconds follow the locale (17:05 for Europe, 5:05 PM for US)
  const hoursMinutesSeconds = hoursMinutesSecondsFormater(d);

  return `${day}/${month}/${year} ${hoursMinutesSeconds}`;
};

const formatTimeHMS = d3.timeFormat("%H:%M:%S");
const formatTimeHM = d3.timeFormat("%H:%M");
const formatTimeFull = d3.timeFormat("%a %d %b");

export const tickFormatDate = (d: Date) => {
  if (d.getMinutes() || d.getHours()) {
    return formatTimeHM(d);
  }

  return formatTimeFull(d);
};

export const tooltipFormatDate = (d: Date) => {
  if (d.getSeconds() || d.getMinutes() || d.getHours()) {
    return formatTimeHMS(d);
  }

  return formatTimeFull(d);
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

export const bitsToString = (bits: number) => {
  const fmt = d3FormaterHandlingNull(".3r");
  const units = ["", "k", "M", "G", "T", "P", "E", "Z", "Y"];

  for (let idx = 0; idx < units.length; idx++) {
    if (bits < Math.pow(1000, idx + 1)) {
      return fmt(bits / Math.pow(1000, idx)) + units[idx] + "bit/s";
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

export const iopsToString = function (iops?: number) {
  return d3FormaterHandlingNull(".3r")(iops) + " IOps";
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

export const secondToString = (value: any) =>
  d3FormaterHandlingNull(".3s", "s")(value);

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

export const formatDateAgo = (date: string | number | Date) => {
  const dateTS =
    date instanceof Date ? date.getTime() : new Date(date).getTime();
  const nowTS = new Date().getTime();
  const diffTS = nowTS - dateTS;
  const diffSecond = Math.floor(diffTS / 1000);
  const diffMinut = Math.floor(diffSecond / 60);
  const diffHour = Math.floor(diffMinut / 60);
  const diffDay = Math.floor(diffHour / 24);
  let plurial = "";
  if (diffDay > 0) {
    if (diffDay > 1) plurial = "s";
    return `${diffDay} day${plurial} ago`;
  } else if (diffHour > 0) {
    if (diffHour % 24 > 1) plurial = "s";
    return `${diffHour % 24} hour${plurial} ago`;
  } else if (diffMinut > 0) {
    if (diffMinut % 60 > 1) plurial = "s";
    return `${diffMinut % 60} minute${plurial} ago`;
  } else {
    return "a moment ago";
  }
};

export function _formatCpuTime(time: number) {
  const minutes = Math.trunc(time / 60);
  return `${minutes}:${d3.format(".2f")(time % 60)}`;
}

export const formatMetricName = (metricName: string) => {
  metricName = metricName.replace(/_used_perc$/, "");
  const metricNameSplitted = metricName.split("_");
  let result = "";
  metricNameSplitted.forEach((metricNamePart: string) => {
    result +=
      metricNamePart.substring(0, 1).toUpperCase() +
      metricNamePart.substring(1) +
      " ";
  });
  return result;
};
