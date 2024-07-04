/* eslint-disable camelcase */
import * as d3 from "d3";
import React, { useState } from "react";
import PropTypes from "prop-types";
import { Grid } from "tabler-react";
import { Tooltip } from "react-tooltip";
import Panel from "../UI/Panel";
import { cssClassForStatus, textForStatus } from "../utils/converter";
import FaIcon from "../UI/FaIcon";
import ServiceDetailsModal from "../Service/ServiceDetailsModal";
import { useHTTPDataFetch } from "../utils/hooks";
import { formatDateTimeWithSeconds } from "../utils/formater";
import Smiley from "../UI/Smiley";
import { Problems, isNullOrUndefined } from "../utils";
import FetchSuspense from "../UI/FetchSuspense";
import { AGENT_DETAILS } from "../utils/gqlRequests";
import { SERVICES_URL } from "../utils/dataRoutes";
import { AGENT_INFORMATIONS_URL } from "../utils/dataRoutes";
import { AGENT_STATUS_URL } from "../utils/dataRoutes";
import { TAGS_URL } from "../utils/dataRoutes";
import "react-tooltip/dist/react-tooltip.css";

const AgentDetails = ({ facts }) => {
  const [showServiceDetails, setShowServiceDetails] = useState(null);

  const factUpdatedAt = facts.find((f) => f.name === "fact_updated_at").value;
  let factUpdatedAtDate;
  if (factUpdatedAt) {
    factUpdatedAtDate = new Date(factUpdatedAt);
  }

  let expireAgentBanner = null;
  let agentDate = null;
  const agentVersion = facts.find((f) => f.name === "glouton_version").value;
  if (agentVersion) {
    const expDate = new Date();
    expDate.setDate(expDate.getDate() - 60);
    // First try new format (e.g. 18.03.21.134432)
    agentDate = d3.timeParse(agentVersion.slice(0, 15));
    if (!agentDate) {
      // then old format (0.20180321.134432)
      agentDate = d3.timeParse(agentVersion.slice(0, 17));
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

  let serviceDetailsModal = null;
  if (showServiceDetails) {
    serviceDetailsModal = (
      <ServiceDetailsModal
        service={showServiceDetails}
        closeAction={() => setShowServiceDetails(null)}
      />
    );
  }

  const { isLoading: isLoadingServices, error: errorServices, data: servicesData } = useHTTPDataFetch(SERVICES_URL, null, 60000);
  const { isLoading: isLoadingTags, error: errorTags, data: tagsData } = useHTTPDataFetch(TAGS_URL, null, 60000);
  const { isLoading: isLoadingAgentInformation, error: errorAgentInformation, data: agentInformationData } = useHTTPDataFetch(AGENT_INFORMATIONS_URL, null, 60000);
  const { isLoading: isLoadingAgentStatus, error: errorAgentStatus, data: agentStatusData } = useHTTPDataFetch(AGENT_STATUS_URL, null, 60000);
  const isLoading = isLoadingServices || isLoadingTags || isLoadingAgentInformation || isLoadingAgentStatus;
  const error = errorServices || errorTags || errorAgentInformation || errorAgentStatus;
  
  const services = servicesData;
  const tags = tagsData;
  const agentInformation = agentInformationData;
  const agentStatus = agentStatusData;

  let tooltipType = "info";
  let problems = null;
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
        {agentStatus.statusDescription && agentStatus.statusDescription.length > 0 ? (
          <Tooltip
            place="bottom"
            anchorId="agentStatus"
            effect="solid"
            type={tooltipType}
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

      <Grid.Row>
        <Grid.Col xl={4}>
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
                              // TODO : Change in favor of service status
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
                  new Date(agentInformation.lastReport).getFullYear() !== 1 ? (
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
        </Grid.Col>
        <Grid.Col x={4}>
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
                  Tags for {facts.find((f) => f.name === "fqdn").value}:
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
          <Grid.Row>
            <Grid.Col xl={6} offset={3}>
              {problems ? <Panel>{problems}</Panel> : null}
            </Grid.Col>
          </Grid.Row>
        </Grid.Col>
        <Grid.Col xl={4}>
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
        </Grid.Col>
      </Grid.Row>
    </div>
  );
};

AgentDetails.propTypes = {
  facts: PropTypes.instanceOf(Array).isRequired,
};

export default AgentDetails;
