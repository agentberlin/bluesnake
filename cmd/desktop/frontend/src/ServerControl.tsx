// Copyright 2025 Agentic World, LLC (Sherin Thomas)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import { useState, useEffect } from 'react';
import { StartServerWithTunnel, StopServerWithTunnel, GetServerStatus } from "../wailsjs/go/main/DesktopApp";
import './ServerControl.css';

interface ServerStatus {
  isRunning: boolean;
  publicURL: string;
  port: number;
  error?: string;
}

interface ServerInfo {
  publicURL: string;
  port: number;
}

function ServerControl() {
  const [status, setStatus] = useState<ServerStatus>({
    isRunning: false,
    publicURL: '',
    port: 0
  });
  const [isLoading, setIsLoading] = useState(false);
  const [showCopied, setShowCopied] = useState(false);

  // Poll server status
  useEffect(() => {
    const loadStatus = async () => {
      try {
        const serverStatus = await GetServerStatus();
        setStatus(serverStatus);
      } catch (error) {
        console.error('Failed to get server status:', error);
      }
    };

    // Initial load
    loadStatus();

    // Poll every 2 seconds when server is running
    if (status.isRunning) {
      const interval = setInterval(loadStatus, 2000);
      return () => clearInterval(interval);
    }
  }, [status.isRunning]);

  const handleStart = async () => {
    setIsLoading(true);
    try {
      const serverInfo: ServerInfo = await StartServerWithTunnel();
      setStatus({
        isRunning: true,
        publicURL: serverInfo.publicURL,
        port: serverInfo.port
      });
    } catch (error: any) {
      console.error('Failed to start server:', error);
      setStatus({
        isRunning: false,
        publicURL: '',
        port: 0,
        error: error.toString()
      });
    } finally {
      setIsLoading(false);
    }
  };

  const handleStop = async () => {
    setIsLoading(true);
    try {
      await StopServerWithTunnel();
      setStatus({
        isRunning: false,
        publicURL: '',
        port: 0
      });
    } catch (error: any) {
      console.error('Failed to stop server:', error);
      setStatus({
        ...status,
        error: error.toString()
      });
    } finally {
      setIsLoading(false);
    }
  };

  const handleCopyURL = () => {
    navigator.clipboard.writeText(status.publicURL);
    setShowCopied(true);
    setTimeout(() => setShowCopied(false), 2000);
  };

  const truncateUrl = (url: string): string => {
    if (url.length <= 40) return url;
    return url.substring(0, 37) + '...';
  };

  return (
    <>
      <div className="server-control-container">
        {!status.isRunning ? (
          <>
            <button
              className="server-control-button"
              onClick={handleStart}
              disabled={isLoading}
              title="Start local server"
            >
              {isLoading ? 'Starting...' : 'Start Server'}
            </button>
            {status.error && (
              <div className="server-error">
                {status.error}
              </div>
            )}
          </>
        ) : (
          <div className="server-running-inline">
            <span className="server-status-indicator"></span>
            <a
              href={status.publicURL}
              target="_blank"
              rel="noopener noreferrer"
              className="server-url"
              title={status.publicURL}
            >
              {truncateUrl(status.publicURL)}
            </a>
            <button
              className="server-copy-button"
              onClick={handleCopyURL}
              title={showCopied ? "Copied!" : "Copy URL"}
            >
              {showCopied ? (
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="20 6 9 17 4 12"></polyline>
                </svg>
              ) : (
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                  <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                </svg>
              )}
            </button>
            <button
              className="server-stop-button"
              onClick={handleStop}
              disabled={isLoading}
              title="Stop server"
            >
              {isLoading ? (
                <svg className="server-spinner-small" width="12" height="12" viewBox="0 0 16 16">
                  <circle className="server-spinner-circle" cx="8" cy="8" r="6" strokeWidth="2" fill="none" />
                </svg>
              ) : (
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                  <rect x="6" y="6" width="12" height="12" rx="1"></rect>
                </svg>
              )}
            </button>
          </div>
        )}
      </div>
    </>
  );
}

export default ServerControl;
