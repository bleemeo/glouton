import React from "react";
import PropTypes from "prop-types";

import Modal from "../UI/Modal";
import { cssClassForStatus, textForStatus } from "../utils/converter";
import { useHTTPDataFetch } from "../utils/hooks";
import FetchSuspense from "../UI/FetchSuspense";
import { CONTAINERS_URL } from "../utils/dataRoutes";

const ServiceContainer = ({ containerId }) => {
  const { isLoading, error, data } = useHTTPDataFetch(CONTAINERS_URL, { search: containerId });
  const containers = data;

  return (
    <FetchSuspense isLoading={isLoading} error={error} containers={containers}>
      {({ containers }) => {
        if (containers.containers[0]) {
          return (
            <li>
              <strong>Docker:</strong> {containers.containers[0].name}
            </li>
          );
        } else return null;
      }}
    </FetchSuspense>
  );
};

ServiceContainer.propTypes = {
  containerId: PropTypes.string.isRequired,
};

/* eslint-disable react/no-multi-comp */
const ServiceDetails = ({ service }) => {
  if (!service) {
    return null;
  }

  const currentStatus = service.status;

  return (
    <div className="marginOffset">
      <ul className="list-unstyled">
        <li>
          <strong>Current status : </strong>
          <span
            className={`badge badge-${cssClassForStatus(
              currentStatus ? currentStatus : undefined,
            )}`}
          >
            {textForStatus(currentStatus ? currentStatus : undefined)}
          </span>
        </li>
        {service.containerId ? (
          <ServiceContainer containerId={service.containerId} />
        ) : null}
        {!service.listenAddresses ? null : (
          <li>
            <strong>Listen addresses:</strong>{" "}
            {service.listenAddresses.join("\u2003")}
          </li>
        )}
        {service.exePath ? (
          <li>
            <strong>Executable path:</strong> {service.exePath}
          </li>
        ) : null}
        {currentStatus && currentStatus !== 0 ? (
          <li>
            <strong>
              Current Problem:
              <br />
            </strong>{" "}
            {service.statusDescription}
          </li>
        ) : null}
      </ul>
    </div>
  );
};

ServiceDetails.propTypes = {
  service: PropTypes.object.isRequired,
};

export default class ServiceDetailsModal extends React.PureComponent {
  static propTypes = {
    actions: PropTypes.object,
    service: PropTypes.object.isRequired,
    closeAction: PropTypes.func.isRequired,
  };

  render() {
    const { service, closeAction } = this.props;

    if (!service) {
      return null;
    }

    return (
      <Modal
        title={`${service.name}`}
        closeAction={closeAction}
        closeOnBackdropClick
      >
        <ServiceDetails {...this.props} />
      </Modal>
    );
  }
}
