import React, { FC, memo } from "react";

import Modal from "../UI/Modal";
import FetchSuspense from "../UI/FetchSuspense";

import { cssClassForStatus, textForStatus } from "../utils/converter";
import { useHTTPDataFetch } from "../utils/hooks";
import { CONTAINERS_URL } from "../utils/dataRoutes";
import { Containers, Service } from "../Data/data.interface";

type ServiceContainerProps = {
  containerId: string;
};

const ServiceContainer: FC<ServiceContainerProps> = ({ containerId }) => {
  const { isLoading, error, data } = useHTTPDataFetch<Containers>(
    CONTAINERS_URL,
    {
      search: containerId,
    },
  );
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

type ServiceDetailsProps = {
  service: Service;
};

const ServiceDetails: FC<ServiceDetailsProps> = ({ service }) => {
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

type ServiceDetailsModalProps = {
  service: Service;
  closeAction: () => void;
};

const ServiceDetailsModal = memo(function ServiceDetailsModal({
  service,
  closeAction,
}: ServiceDetailsModalProps) {
  if (!service) {
    return null;
  }

  return (
    <Modal title={`${service.name}`} closeAction={closeAction}>
      <ServiceDetails service={service} />
    </Modal>
  );
});

export default ServiceDetailsModal;
