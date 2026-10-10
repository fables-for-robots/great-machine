import { render } from "solid-js/web";
import App from "./App";
import "./styles/fonts.css";
import "./styles/tokens.css";
import "./styles/base.css";

const root = document.getElementById("root");
if (!root) {
  throw new Error("index.html has no element with the id root");
}
render(() => <App />, root);
