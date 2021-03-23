import classnames from "classnames";
import React from "react";
import PropTypes from "prop-types";
import DayPicker, { DateUtils } from "react-day-picker";
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

const formatTime = (date) => {
  let hours = date.getHours();
  hours = hours < 10 ? "0" + hours : hours.toString();
  let minutes = date.getMinutes();
  minutes = minutes < 10 ? "0" + minutes : minutes.toString();
  return `${hours}:${minutes}`;
};

export const lastQuickRanges = [
  { value: 60, label: "Last 1 hour" },
  { value: 360, label: "Last 6 hours" },
  { value: 1440, label: "Last day" },
  { value: 10080, label: "Last 7 days" },
];

const relativeQuickRanges = [{ value: "yesterday", label: "Yesterday" }];

class DateTimeColumn extends React.PureComponent {
  state = {};
  render() {
    const {
      label,
      value,
      timeValue,
      hasError,
      onDayClick,
      selectedDays,
      onTimeChange,
    } = this.props;

    return (
      <div className="col-sm-6">
        <div className="mx-3">{label}:</div>
        <DayPicker
          selectedDays={selectedDays}
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
  }
}

DateTimeColumn.propTypes = {
  label: PropTypes.string.isRequired,
  value: PropTypes.instanceOf(Date).isRequired,
  timeValue: PropTypes.string.isRequired,
  hasError: PropTypes.bool,
  onDayClick: PropTypes.func.isRequired,
  selectedDays: PropTypes.func.isRequired,
  onTimeChange: PropTypes.func.isRequired,
};

/* eslint-disable react/jsx-no-bind */
export default class EditPeriodModal extends React.Component {
  static propTypes = {
    period: PropTypes.object.isRequired, // eslint-disable-line react/no-unused-prop-types
    onPeriodChange: PropTypes.func.isRequired,
    onClose: PropTypes.func.isRequired,
  };

  constructor(props) {
    super(props);
    this.state = {
      isQuickTabActive:
        !isNullOrUndefined(props.period.minutes) &&
        isNullOrUndefined(props.period.to) &&
        isNullOrUndefined(props.period.from)
          ? "0"
          : "1",
    };
  }

  static getDerivedStateFromProps(props, state) {
    if (state.from === undefined && state.to === undefined) {
      const { from, to, minutes } = props.period;
      const now = new Date();
      let _from = from ? new Date(from) : null;
      if (!_from && minutes) {
        _from = new Date();
        _from.setUTCMinutes(_from.getUTCMinutes() - minutes);
      }
      const _to = to ? new Date(to) : now;
      const fromTime = formatTime(_from);
      const toTime = formatTime(_to);
      return {
        from: _from,
        to: _to,
        fromTime,
        toTime,
      };
    }
    return null;
  }

  _onMainAction = () => {
    const { onPeriodChange, onClose } = this.props;
    const { from, to, fromHasError, toHasError } = this.state;
    if (!fromHasError && !toHasError && this.checkDates(from, to)) {
      onPeriodChange({ from, to });
      onClose();
    }
  };

  _onCloseAction = () => {
    const { onClose } = this.props;
    onClose();
  };

  checkDates = (from, to) => {
    if (to < from) {
      this.setState({
        error: "You cannot have the first date after the second one.",
      });
      return false;
    }
    // we need 7 days & 1 hour & 1 minute to allow 7 days with a change of saving day light in the middle
    const maxPastDate = new Date(to);
    maxPastDate.setDate(maxPastDate.getDate() - 7);
    maxPastDate.setHours(maxPastDate.getHours() - 1);
    maxPastDate.setMinutes(maxPastDate.getMinutes() - 1);
    if (from < maxPastDate) {
      this.setState({ durationTooLarge: true });
      return false;
    }
    this.setState({ error: undefined, durationTooLarge: false });
    return true;
  };

  onDayClick = (fromOrTo, day) => {
    const { from, to } = this.state;
    const range = { from: new Date(from), to: new Date(to) };
    range[fromOrTo] = day;

    // we keep previous hours & minutes
    range.from.setHours(from.getHours());
    range.from.setMinutes(from.getMinutes());
    range.to.setHours(to.getHours());
    range.to.setMinutes(to.getMinutes());
    this.checkDates(range.from, range.to);
    if (range.from < range.to) {
      this.setState(range);
    }
  };

  onTimeChange = (fromOrTo, value) => {
    try {
      this.setState({ [fromOrTo + "Time"]: value });
      const groups = /^(\d\d):(\d\d)$/.exec(value);
      if (groups === null) {
        throw new Error();
      }
      let hours = groups[1];
      let minutes = groups[2];
      hours = parseInt(hours);
      if (isNaN(hours) || hours < 0 || hours > 23) {
        throw new Error();
      }
      minutes = parseInt(minutes);
      if (isNaN(minutes) || minutes < 0 || minutes > 59) {
        throw new Error();
      }

      const date = new Date(this.state[fromOrTo]);
      date.setHours(hours);
      date.setMinutes(minutes);
      const from = fromOrTo === "from" ? date : this.state.from;
      const to = fromOrTo === "from" ? this.state.to : date;
      this.checkDates(from, to);
      this.setState({ [fromOrTo]: date, [fromOrTo + "HasError"]: false });
    } catch (e) {
      this.setState({ [fromOrTo + "HasError"]: true });
    }
  };

  handleLastQuickRange = (minutes) => {
    const { onPeriodChange, onClose } = this.props;
    onPeriodChange({ minutes });
    onClose();
  };

  handleRelativeQuickRange = (name) => {
    const { onPeriodChange, onClose } = this.props;
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

  handleApply = () => {
    const { onPeriodChange, onClose } = this.props;
    onPeriodChange({ from: this.state.from, to: this.state.to });
    onClose();
  };

  render() {
    const {
      from,
      fromTime,
      fromHasError,
      to,
      toTime,
      toHasError,
      durationTooLarge,
      error,
      isQuickTabActive,
    } = this.state;

    return (
      <Modal
        title="Period"
        mainBtnAction={this._onMainAction}
        closeAction={this._onCloseAction}
        closeLabel="Cancel"
        size="lg"
      >
        <div className="marginOffset">
          <Nav tabs>
            <NavItem
              active={isQuickTabActive === "0"}
              onClick={() => this.setState({ isQuickTabActive: "0" })}
            >
              <NavLink href="#quick" active={isQuickTabActive === "0"}>
                Quick Periods
              </NavLink>
            </NavItem>
            <NavItem
              active={isQuickTabActive === "1"}
              onClick={() => this.setState({ isQuickTabActive: "1" })}
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
                    <a
                      href="#"
                      onClick={() => this.handleLastQuickRange(range.value)}
                    >
                      {range.label}
                    </a>
                  </div>
                ))}
                {relativeQuickRanges.map((range) => (
                  <div key={range.value}>
                    <a
                      href="#"
                      onClick={() => this.handleRelativeQuickRange(range.value)}
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
                    value={from}
                    timeValue={fromTime}
                    hasError={fromHasError}
                    onDayClick={this.onDayClick.bind(this, "from")}
                    selectedDays={(day) => DateUtils.isSameDay(day, from)}
                    onTimeChange={this.onTimeChange.bind(this, "from")}
                  />
                  <DateTimeColumn
                    label="To"
                    value={to}
                    timeValue={toTime}
                    hasError={toHasError}
                    onDayClick={this.onDayClick.bind(this, "to")}
                    selectedDays={(day) => DateUtils.isSameDay(day, to)}
                    onTimeChange={this.onTimeChange.bind(this, "to")}
                  />
                </div>
              </form>
              {error ? <div className="my-3 text-danger">{error}</div> : null}
              <div className="text-right">
                <button className="btn btn-primary" onClick={this.handleApply}>
                  Apply
                </button>
              </div>
            </TabPane>
          </TabContent>
        </div>
      </Modal>
    );
  }
}
