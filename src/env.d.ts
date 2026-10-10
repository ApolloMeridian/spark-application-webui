/// <reference types="vite/client" />

interface Window {
  __SPARK_CONTROL_CENTER_CONFIG__?: import('./runtimeConfig').RuntimeConfigInput;
}
