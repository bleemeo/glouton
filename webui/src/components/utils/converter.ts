export const badgeColorSchemeForStatus = (
  status: number | string | undefined,
) => {
  switch (status) {
    case 0:
    case "OK":
      return "green";
    case 1:
    case "WARNING":
      return "yellow";
    case 2:
    case "CRITICAL":
      return "red";
    // STATUS_UNKNOWN = 3
    case 10:
    case "INFO":
      return "blue";
    default:
      return "green"; // or 'blue' ??
  }
};

// TODO to remove once the API branch PRODUCT-101 has been merge into master
export const textForStatus = (status: number | string | undefined) => {
  switch (status) {
    case 0:
      return "OK";
    case 1:
      return "WARNING";
    case 2:
      return "CRITICAL";
    // STATUS_UNKNOWN = 3
    case 10:
      return "INFO";
    default:
      return "OK";
  }
};

export const colorForStatus = (status: number | string) => {
  switch (status) {
    case 0:
      return "28A745";
    case 1:
      return "f0ad4e";
    case 2:
      return "d9534f";
    // STATUS_UNKNOWN = 3
    default:
      return "28A745";
  }
};
