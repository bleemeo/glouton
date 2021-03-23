export const computeBackwardForward = (
  nbMinutes: number,
  startDate: Date,
  endDate: Date,
  isForward = false
): unknown => {
  const applyBackwardOrForward = isForward
    ? nbMinutes * 0.9
    : 0 - nbMinutes * 0.9;
  startDate.setUTCMinutes(startDate.getUTCMinutes() + applyBackwardOrForward);
  endDate.setUTCMinutes(endDate.getUTCMinutes() + applyBackwardOrForward);
  return { startDate, endDate };
};
