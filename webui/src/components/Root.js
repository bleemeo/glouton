import React from "react";
import { ApolloProvider } from "@apollo/react-hooks";
import Routes from "./Routes";
import TopNavBar from "./App/TopNavBar";
import client from "../utils/API";

const Root = () => {
  return (
    <ApolloProvider client={client}>
      <div className="marginOffset">
        <TopNavBar />
        <div className="main-content">
          <Routes />
        </div>
      </div>
    </ApolloProvider>
  );
};

export default Root;
