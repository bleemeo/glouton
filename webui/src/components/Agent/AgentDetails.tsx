/* eslint-disable camelcase */
import React, { FC, useState } from "react";
import * as d3 from "d3";
import { Tooltip } from "react-tooltip";
import "react-tooltip/dist/react-tooltip.css";
import { AxiosError } from "axios";

import Panel from "../UI/Panel";
import FaIcon from "../UI/FaIcon";
import Smiley from "../UI/Smiley";
import FetchSuspense from "../UI/FetchSuspense";
import ServiceDetailsModal from "../Service/ServiceDetailsModal";
import Row from "react-bootstrap/Row";
import Col from "react-bootstrap/Col";
import Container from "react-bootstrap/Container";

import { useHTTPDataFetch } from "../utils/hooks";
import { isNullOrUndefined, Problems } from "../utils";
import { cssClassForStatus, textForStatus } from "../utils/converter";
import { formatDateTimeWithSeconds } from "../utils/formater";
import {
  SERVICES_URL,
  AGENT_INFORMATIONS_URL,
  AGENT_STATUS_URL,
  TAGS_URL,
} from "../utils/dataRoutes";
import {
  AgentInfo,
  AgentStatus,
  Fact,
  Service,
  Tag,
} from "../Data/data.interface";

type AgentDetailsProps = {
  facts: Fact[];
};

const AgentDetails: FC<AgentDetailsProps> = ({ facts }) => {
  const [showServiceDetails, setShowServiceDetails] = useState<Service | null>(
    null,
  );

  const factUpdatedAt: string | undefined = facts.find(
    (f: { name: string }) => f.name === "fact_updated_at",
  )?.value;

  let factUpdatedAtDate: Date | null = null;

  if (factUpdatedAt) {
    factUpdatedAtDate = new Date(factUpdatedAt);
  }

  let expireAgentBanner: JSX.Element | null = null;
  let agentDate: Date | null = null;

  const agentVersion: string | undefined = facts.find(
    (f: { name: string }) => f.name === "glouton_version",
  )?.value;

  if (agentVersion) {
    const expDate: Date = new Date();
    expDate.setDate(expDate.getDate() - 60);

    // First try new format (e.g. 18.03.21.134432)
    agentDate = d3.timeParse(".%L")(agentVersion.slice(0, 15));
    if (!agentDate) {
      // then old format (0.20180321.134432)
      agentDate = d3.timeParse(".%L")(agentVersion.slice(0, 17));
    }

    if (agentDate && agentDate < expDate) {
      expireAgentBanner = (
        <div className="row">
          <div className="col-xl-12">
            <div className="alert alert-danger" role="alert">
              This agent is more than 60 days old. You should update it. See the
              <a
                href="https://go.bleemeo.com/l/agent-upgrade"
                target="_blank"
                rel="noopener noreferrer"
              >
                {" "}
                documentation
              </a>
              &nbsp; to learn how to do it.
            </div>
          </div>
        </div>
      );
    }
  }

  let serviceDetailsModal: JSX.Element | null = null;
  if (showServiceDetails) {
    serviceDetailsModal = (
      <ServiceDetailsModal
        service={showServiceDetails}
        closeAction={() => setShowServiceDetails(null)}
      />
    );
  }

  const {
    isLoading: isLoadingServices,
    error: errorServices,
    data: servicesData,
  } = useHTTPDataFetch<Service>(SERVICES_URL, null, 60000);

  const {
    isLoading: isLoadingTags,
    error: errorTags,
    data: tagsData,
  } = useHTTPDataFetch<Tag>(TAGS_URL, null, 60000);

  const {
    isLoading: isLoadingAgentInformation,
    error: errorAgentInformation,
    data: agentInformationData,
  } = useHTTPDataFetch<AgentInfo>(AGENT_INFORMATIONS_URL, null, 60000);

  const {
    isLoading: isLoadingAgentStatus,
    error: errorAgentStatus,
    data: agentStatusData,
  } = useHTTPDataFetch<AgentStatus>(AGENT_STATUS_URL, null, 60000);

  const isLoading: boolean =
    isLoadingServices ||
    isLoadingTags ||
    isLoadingAgentInformation ||
    isLoadingAgentStatus;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const error: AxiosError<unknown, any> | null =
    errorServices || errorTags || errorAgentInformation || errorAgentStatus;

  const services: Service | null = servicesData;
  const tags: Tag | null = tagsData;
  const agentInformation: AgentInfo | null = agentInformationData;
  const agentStatus: AgentStatus | null = agentStatusData;

  let tooltipType: string = "info";
  let problems: JSX.Element | null = null;

  if (agentStatus) {
    switch (agentStatus.status) {
      case 0:
        tooltipType = "success";
        break;
      case 1:
        tooltipType = "warning";
        break;
      case 2:
        tooltipType = "error";
        break;
      default:
        tooltipType = "info";
        break;
    }

    problems = (
      <div className="marginOffset">
        <p className="text-center" id="agentStatus">
          <Smiley status={agentStatus.status} />
        </p>
        <p className="text-center">{textForStatus(agentStatus.status)}</p>
        {agentStatus.statusDescription &&
        agentStatus.statusDescription.length > 0 ? (
          <Tooltip
            place="bottom"
            anchorSelect="agentStatus"
            // effect="solid"
            data-tooltip-variant={tooltipType}
          >
            <div style={{ maxWidth: "80rem", wordBreak: "break-all" }}>
              <Problems problems={agentStatus.statusDescription} />
            </div>
          </Tooltip>
        ) : null}
      </div>
    );
  }

  return (
    <div id="page-wrapper" style={{ marginTop: "1.5rem" }}>
      {serviceDetailsModal}

      {expireAgentBanner}

      <Container>
        <Row>
          <Col sm={4}>
            <FetchSuspense
              isLoading={isLoading}
              error={
                error ||
                isNullOrUndefined(services) ||
                isNullOrUndefined(tags) ||
                isNullOrUndefined(agentInformation) ||
                isNullOrUndefined(agentStatus)
              }
              services={services}
            >
              {({ services }) => (
                <Panel>
                  <div className="marginOffset">
                    Services running on this agent:
                    <ul className="list-inline">
                      {services
                        .filter((service) => service.active)
                        .sort((a, b) => a.name.localeCompare(b.name))
                        .map((service, idx) => {
                          return (
                            <li key={idx} className="list-inline-item">
                              <button
                                className={`btn blee-label btn-${cssClassForStatus(
                                  service.status,
                                )}`}
                                onClick={() => setShowServiceDetails(service)}
                              >
                                {service.name}
                                &nbsp;
                                <FaIcon icon="fas fa-info-circle" />
                              </button>
                            </li>
                          );
                        })}
                    </ul>
                  </div>
                </Panel>
              )}
            </FetchSuspense>
            {agentInformation && Object.keys(agentInformation).length > 0 ? (
              <Panel>
                <div className="marginOffset">
                  <ul className="list-unstyled">
                    {agentInformation.registrationAt &&
                    new Date(agentInformation.registrationAt).getFullYear() !==
                      1 ? (
                      <li>
                        <b>Glouton registration at:</b>{" "}
                        {formatDateTimeWithSeconds(
                          agentInformation.registrationAt,
                        )}
                      </li>
                    ) : null}
                    {agentInformation.lastReport &&
                    new Date(agentInformation.lastReport).getFullYear() !==
                      1 ? (
                      <li>
                        <b>Glouton last report:</b>{" "}
                        {formatDateTimeWithSeconds(agentInformation.lastReport)}
                      </li>
                    ) : null}
                    <li>
                      <div className="d-flex flex-row align-items-center">
                        <b style={{ marginRight: "0.4rem" }}>
                          Connected to Bleemeo ?
                        </b>
                        <div
                          className={
                            agentInformation.isConnected
                              ? "isConnected"
                              : "isNotConnected"
                          }
                        />
                      </div>
                    </li>
                    <li>
                      <div className="d-flex flex-row align-items-center">
                        <b>
                          Need to troubleshoot ?{" "}
                          <a href="/diagnostic">/diagnostic</a> may help you.
                        </b>
                      </div>
                    </li>
                  </ul>
                </div>
              </Panel>
            ) : null}
          </Col>
          <Col sm={4}>
            <FetchSuspense
              isLoading={isLoading}
              error={
                error ||
                isNullOrUndefined(services) ||
                isNullOrUndefined(tags) ||
                isNullOrUndefined(agentInformation) ||
                isNullOrUndefined(agentStatus)
              }
              tags={tags}
            >
              {({ tags }) => (
                <Panel>
                  <div className="marginOffset">
                    Tags for {facts.find((f) => f.name === "fqdn")?.value}:
                    {tags.length > 0 ? (
                      <ul className="list-inline">
                        {tags.map((tag) => (
                          <li key={tag.tagName} className="list-inline-item">
                            <span className="badge badge-info blee-label">
                              {tag.tagName}
                            </span>
                          </li>
                        ))}
                      </ul>
                    ) : (
                      <h4>There is no tag to display</h4>
                    )}
                  </div>
                </Panel>
              )}
            </FetchSuspense>
            <Row>
              <Col xl={6} offset={3}>
                {problems ? <Panel>{problems}</Panel> : null}
              </Col>
            </Row>
          </Col>
          <Col sm={4}>
            <Panel>
              <div className="marginOffset">
                <h5>Information retrieved from the agent</h5>
                {factUpdatedAtDate ? (
                  <p>(last update: {factUpdatedAtDate.toLocaleString()})</p>
                ) : null}
                <ul className="list-unstyled">
                  {facts
                    .filter((f) => f.name !== "fact_updated_at")
                    .sort((a, b) => a.name.localeCompare(b.name))
                    .map((fact) => (
                      <li key={fact.name}>
                        <b>{fact.name}:</b> {fact.value}
                      </li>
                    ))}
                </ul>
              </div>
            </Panel>
          </Col>
        </Row>
      </Container>
    </div>
  );
};

export default AgentDetails;
