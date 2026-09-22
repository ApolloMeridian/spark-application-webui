import React from 'react';
import ReactDOM from 'react-dom/client';
import { App } from './App';
import { runtimeConfig } from './runtimeConfig';
import './styles.css';

document.title = runtimeConfig.appName;

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
