/* eslint-disable @typescript-eslint/no-explicit-any */
import classnames from "classnames";
import React, { memo, useState } from "react";
import { DayPicker } from "react-day-picker";
import { Form } from "tabler-react";
import {
  Nav,
  NavItem,
  NavLink,
  TabContent,
  TabPane,
  Dropdown,
  DropdownItem,
  DropdownMenu,
  DropdownToggle,
} from "reactstrap";
import "react-day-picker/lib/style.css";

import Modal from "../UI/Modal";
import { formatDate } from "../utils/formater";
import { isNullOrUndefined } from "../utils";
import FaIcon from "../UI/FaIcon";

export const lastQuickRanges = [
  { value: 60, label: "Last 1 hour" },
  { value: 360, label: "Last 6 hours" },
  { value: 1440, label: "Last day" },
  { value: 10080, label: "Last 7 days" },
];

const relativeQuickRanges = [{ value: "yesterday", label: "Yesterday" }];

type DateTimeColumnProps = {
  label: string;
  value: Date;
  timeValue: string;
  hasError: boolean;
  onDayClick: (day: Date) => void;
  selectedDays: (day: Date) => boolean;
  onTimeChange: (value: string) => void;
};

const DateTimeColumn = memo(function DateTimeColumn({
  label,
  value,
  timeValue,
  hasError,
  onDayClick,
  selectedDays,
  onTimeChange,
}: DateTimeColumnProps) {
  return (
    <div className="col-sm-6">
      <div className="mx-3">{label}:</div>
      <DayPicker
        selected={selectedDays}
        onDayClick={onDayClick}
        month={value}
      />
      <div
        className={classnames("blee-row form-group mx-3", {
          "has-danger": hasError,
        })}
      >
        <span
          style={{
            float: "left",
            marginTop: "0.5rem",
            marginRight: "0.5rem",
          }}
        >
          {formatDate(value)}
        </span>
        <Form.MaskedInput
          style={{ width: "4.5rem", float: "left", marginRight: "0.5rem" }}
          value={timeValue}
          placeholder="00:00"
          onChange={(e) => onTimeChange(e.target.value)}
          mask={[/\d/, /\d/, ":", /\d/, /\d/]}
        />
        <Dropdown
          isOpen={this.state.dropdownOpen}
          toggle={() => {
            this.setState((prevState) => ({
              dropdownOpen: !prevState.dropdownOpen,
            }));
          }}
        >
          <DropdownToggle tag="a">
            <FaIcon icon="far fa-clock fa-2x" />
          </DropdownToggle>
          <DropdownMenu className="dropdownMenuSmall">
            <DropdownItem
              className="btn-outline-dark"
              onClick={() => {
                const now = new Date();
                onTimeChange(now.getHours() + ":" + now.getMinutes());
              }}
            >
              Current Time
            </DropdownItem>
            <DropdownItem
              className="btn-outline-dark"
              onClick={() => onTimeChange("00:00")}
            >
              Midnight
            </DropdownItem>
            <DropdownItem
              className="btn-outline-dark"
              onClick={() => onTimeChange("06:00")}
            >
              6 AM
            </DropdownItem>
            <DropdownItem
              className="btn-outline-dark"
              onClick={() => onTimeChange("12:00")}
            >
              Midday
            </DropdownItem>
            <DropdownItem
              className="btn-outline-dark"
              onClick={() => onTimeChange("18:00")}
            >
              6 PM
            </DropdownItem>
            <DropdownItem
              className="btn-outline-dark"
              onClick={() => onTimeChange("23:59")}
            >
              Endday
            </DropdownItem>
          </DropdownMenu>
        </Dropdown>
      </div>
    </div>
  );
});

type EditPeriodModalProps = {
  period: {
    from: Date;
    to: Date;
    minutes: number;
  };
  onPeriodChange: (period: {
    from?: Date | null;
    to?: Date | null;
    minutes?: number | null;
  }) => void;
  onClose: () => void;
};
/* eslint-disable react/jsx-no-bind */
const EditPeriodModal: React.FC<EditPeriodModalProps> = ({
  period,
  onPeriodChange,
  onClose,
}) => {
  const [isQuickTabActive, setIsQuickTabActive] = useState(
    !isNullOrUndefined(period.minutes) &&
      isNullOrUndefined(period.to) &&
      isNullOrUndefined(period.from)
      ? "0"
      : "1",
  );
  const [from, setFrom] = useState<Date | null>(null);
  const [to, setTo] = useState<Date | null>(null);
  const [fromTime, setFromTime] = useState<string>("");
  const [toTime, setToTime] = useState<string>("");
  const [fromHasError, setFromHasError] = useState<boolean>(false);
  const [toHasError, setToHasError] = useState<boolean>(false);
  const [error, setError] = useState<string | undefined>(undefined);
  const [durationTooLarge, setDurationTooLarge] = useState<boolean>(false);
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const [range, setRange] = useState<{ from: Date | null; to: Date | null }>({
    from: null,
    to: null,
  });

  const checkDates = (
    from: number | Date | null,
    to: string | number | Date | null,
  ) => {
    if (from === null || to === null) {
      return false;
    }
    if (to < from) {
      setError("The end date must be after the start date.");
      return false;
    }
    // we need 7 days & 1 hour & 1 minute to allow 7 days with a change of saving day light in the middle
    const maxPastDate = new Date(to);
    maxPastDate.setDate(maxPastDate.getDate() - 7);
    maxPastDate.setHours(maxPastDate.getHours() - 1);
    maxPastDate.setMinutes(maxPastDate.getMinutes() - 1);
    if (from < maxPastDate) {
      setDurationTooLarge(true);
      return false;
    }
    setError(undefined);
    setDurationTooLarge(false);
    return true;
  };

  const _onMainAction = () => {
    if (!fromHasError && !toHasError && checkDates(from, to)) {
      onPeriodChange({ from, to });
      onClose();
    }
  };

  const _onCloseAction = () => {
    onClose();
  };

  const onDayClick = (fromOrTo: string | number, day: any) => {
    if (from === null || to === null) {
      return;
    }
    const range = { from: new Date(from), to: new Date(to) };
    range[fromOrTo] = day;

    // we keep previous hours & minutes
    range.from.setHours(from.getHours());
    range.from.setMinutes(from.getMinutes());
    range.to.setHours(to.getHours());
    range.to.setMinutes(to.getMinutes());
    checkDates(range.from, range.to);
    if (range.from < range.to) {
      setRange(range);
    }
  };

  const onTimeChange = (fromOrTo, value) => {
    try {
      if (fromOrTo === "from") {
        setFromTime(value);
      }
      if (fromOrTo === "to") {
        setToTime(value);
      }
      const groups = /^(\d\d):(\d\d)$/.exec(value);
      if (groups === null) {
        throw new Error();
      }
      const hours = groups[1];
      const minutes = groups[2];
      const hoursS = parseInt(hours);
      if (isNaN(hoursS) || hoursS < 0 || hoursS > 23) {
        throw new Error();
      }
      const minutesS = parseInt(minutes);
      if (isNaN(minutesS) || minutesS < 0 || minutesS > 59) {
        throw new Error();
      }
      const val = fromOrTo === "from" ? from : to;
      const date = val ? new Date(val) : new Date();
      date.setHours(hoursS);
      date.setMinutes(minutesS);
      const fromCheck = fromOrTo === "from" ? date : from;
      const toCheck = fromOrTo === "from" ? to : date;
      checkDates(fromCheck, toCheck);
      if (fromOrTo === "from") {
        setFrom(date);
        setFromHasError(false);
      }
      if (fromOrTo === "to") {
        setTo(date);
        setToHasError(false);
      }
    } catch (e) {
      if (fromOrTo === "from") {
        setFromHasError(true);
      }
      if (fromOrTo === "to") {
        setToHasError(true);
      }
    }
  };

  const handleLastQuickRange = (minutes: any) => {
    onPeriodChange({ minutes });
    onClose();
  };

  const handleRelativeQuickRange = (name: any) => {
    switch (name) {
      case "yesterday": {
        const from = new Date();
        const to = new Date();
        from.setDate(from.getDate() - 1);
        from.setHours(0, 0, 0, 0);
        to.setDate(to.getDate() - 1);
        to.setHours(23, 59, 59, 999);
        onPeriodChange({ from, to });
        onClose();
        break;
      }
      default:
        break;
    }
  };

  const isSameDay = (dateA: Date, dateB: Date) => {
    return (
      dateA?.getFullYear() === dateB?.getFullYear() &&
      dateA?.getMonth() === dateB?.getMonth() &&
      dateA?.getDate() === dateB?.getDate()
    );
  };

  const handleApply = () => {
    onPeriodChange({ from: from, to: to });
    onClose();
  };

  return (
    <Modal
      title="Period"
      mainBtnAction={_onMainAction}
      closeAction={_onCloseAction}
      closeLabel="Cancel"
      size="lg"
    >
      <div className="marginOffset">
        <Nav tabs>
          <NavItem
            active={isQuickTabActive === "0"}
            onClick={() => {
              setIsQuickTabActive("0");
            }}
          >
            <NavLink href="#quick" active={isQuickTabActive === "0"}>
              Quick Periods
            </NavLink>
          </NavItem>
          <NavItem
            active={isQuickTabActive === "1"}
            onClick={() => {
              setIsQuickTabActive("1");
            }}
          >
            <NavLink href="#custom" active={isQuickTabActive === "1"}>
              Custom Period
            </NavLink>
          </NavItem>
        </Nav>
        <TabContent activeTab={isQuickTabActive}>
          <TabPane tabId="0">
            <div style={{ marginTop: "1rem" }}>
              {lastQuickRanges.map((range) => (
                <div key={range.value}>
                  <a href="#" onClick={() => handleLastQuickRange(range.value)}>
                    {range.label}
                  </a>
                </div>
              ))}
              {relativeQuickRanges.map((range) => (
                <div key={range.value}>
                  <a
                    href="#"
                    onClick={() => handleRelativeQuickRange(range.value)}
                  >
                    {range.label}
                  </a>
                </div>
              ))}
            </div>
          </TabPane>
          <TabPane tabId="1">
            <div
              className={classnames("my-3", {
                "text-danger": durationTooLarge,
              })}
            >
              You cannot have more than 7 days between the 2 dates.
            </div>
            <form>
              <div className="row">
                <DateTimeColumn
                  label="From"
                  value={from ? from : new Date()}
                  timeValue={fromTime}
                  hasError={fromHasError}
                  onDayClick={onDayClick.bind(this, "from")}
                  selectedDays={(day) =>
                    isSameDay(day, from ? from : new Date())
                  }
                  onTimeChange={onTimeChange.bind(this, "from")}
                />
                <DateTimeColumn
                  label="To"
                  value={to ? to : new Date()}
                  timeValue={toTime}
                  hasError={toHasError}
                  onDayClick={onDayClick.bind(this, "to")}
                  selectedDays={(day) => isSameDay(day, to ? to : new Date())}
                  onTimeChange={onTimeChange.bind(this, "to")}
                />
              </div>
            </form>
            {error ? <div className="my-3 text-danger">{error}</div> : null}
            <div className="text-right">
              <button className="btn btn-primary" onClick={handleApply}>
                Apply
              </button>
            </div>
          </TabPane>
        </TabContent>
      </div>
    </Modal>
  );
};

export default EditPeriodModal;
