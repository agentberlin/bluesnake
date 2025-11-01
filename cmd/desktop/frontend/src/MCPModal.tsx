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

import React, { useState, useEffect } from 'react';
import { Modal, ModalContent, Button, Icon, Badge } from './design-system';
import { GetMCPServerStatus } from '../wailsjs/go/main/DesktopApp';
import './MCPModal.css';

interface MCPModalProps {
  isOpen: boolean;
  onClose: () => void;
  serverUrl?: string;
  onStopServer?: () => void;
}

const detectPlatform = (): 'macOS' | 'Windows' | 'Linux' => {
  const platform = window.navigator.platform.toLowerCase();

  if (platform.includes('mac')) return 'macOS';
  if (platform.includes('win')) return 'Windows';
  return 'Linux';
};

const getConfigPathForPlatform = (platform: string) => {
  const configs = {
    'macOS': '~/Library/Application Support/Claude/claude_desktop_config.json',
    'Windows': '%APPDATA%\\Claude\\claude_desktop_config.json',
    'Linux': '~/.config/Claude/claude_desktop_config.json'
  };

  return configs[platform as keyof typeof configs];
};

type ConfigTab = 'desktop' | 'code' | 'web';

export const MCPModal: React.FC<MCPModalProps> = ({ isOpen, onClose, serverUrl: propServerUrl, onStopServer }) => {
  const [copied, setCopied] = useState(false);
  const [activeTab, setActiveTab] = useState<ConfigTab>('web');
  const [serverUrl, setServerUrl] = useState<string>(propServerUrl || '');
  const [isServerRunning, setIsServerRunning] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const platform = detectPlatform();
  const configPath = getConfigPathForPlatform(platform);

  // Fetch server status when modal opens
  useEffect(() => {
    if (isOpen) {
      setIsLoading(true);
      GetMCPServerStatus()
        .then((status: any) => {
          setIsServerRunning(status.running);
          if (status.url) {
            setServerUrl(status.url);
          }
          setIsLoading(false);
        })
        .catch((error: any) => {
          console.error('Failed to get server status:', error);
          setIsLoading(false);
        });
    }
  }, [isOpen]);

  // Update serverUrl when prop changes
  useEffect(() => {
    if (propServerUrl) {
      setServerUrl(propServerUrl);
      setIsServerRunning(true);
    }
  }, [propServerUrl]);

  const desktopConfig = JSON.stringify(
    {
      mcpServers: {
        bluesnake: {
          command: 'npx',
          args: ['-y', 'mcp-remote', serverUrl]
        }
      }
    },
    null,
    2
  );

  const codeConfig = JSON.stringify(
    {
      mcpServers: {
        bluesnake: {
          url: serverUrl,
          transport: 'streamable'
        }
      }
    },
    null,
    2
  );

  const handleCopyConfig = async (config: string) => {
    try {
      await navigator.clipboard.writeText(config);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (error) {
      console.error('Failed to copy configuration:', error);
    }
  };

  const handleCopyUrl = async () => {
    try {
      await navigator.clipboard.writeText(serverUrl);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (error) {
      console.error('Failed to copy URL:', error);
    }
  };

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title="Connect BlueSnake to Claude AI"
      size="large"
      closeOnOverlayClick={true}
      closeOnEscape={true}
      showCloseButton={true}
    >
      <ModalContent>
        <div className="mcp-modal-intro">
          <p>Access BlueSnake's web crawling capabilities from Claude AI, Claude Code, and other AI assistants.</p>
        </div>

        {isLoading ? (
          <div className="mcp-loading">
            <p>Checking server status...</p>
          </div>
        ) : !isServerRunning ? (
          <div className="mcp-server-warning">
            <Icon name="alert-triangle" size={20} />
            <p>MCP server is not running. Click "Start MCP Server" to enable access.</p>
          </div>
        ) : (
          <>
            <div className="mcp-tabs">
              <button
                className={`mcp-tab ${activeTab === 'web' ? 'active' : ''}`}
                onClick={() => setActiveTab('web')}
              >
                Connectors
              </button>
              <button
                className={`mcp-tab ${activeTab === 'desktop' ? 'active' : ''}`}
                onClick={() => setActiveTab('desktop')}
              >
                Claude Desktop
              </button>
              <button
                className={`mcp-tab ${activeTab === 'code' ? 'active' : ''}`}
                onClick={() => setActiveTab('code')}
              >
                Claude Code
              </button>
            </div>

            {activeTab === 'desktop' && (
              <div className="mcp-tab-content">
                <div className="mcp-config-section">
                  <h4>Configuration for Claude Desktop</h4>
                  <p className="mcp-tab-description">
                    Requires <code>mcp-remote</code> proxy to connect Claude Desktop to HTTP servers.
                  </p>
                  <pre className="mcp-config-code">
                    <code>{desktopConfig}</code>
                  </pre>
                  <Button
                    variant="primary"
                    size="small"
                    icon={<Icon name={copied ? "check" : "copy"} size={14} />}
                    onClick={() => handleCopyConfig(desktopConfig)}
                  >
                    {copied ? "Copied!" : "Copy Configuration"}
                  </Button>
                </div>

                <div className="mcp-config-location">
                  <h4>Config File Location</h4>
                  <Badge variant="neutral">
                    <code>{configPath}</code>
                  </Badge>
                </div>

                <div className="mcp-instructions">
                  <h4>Setup Instructions</h4>
                  <ol>
                    <li>Copy the configuration above</li>
                    <li>Open your Claude Desktop config file at the location shown</li>
                    <li>Add the configuration under the <code>mcpServers</code> key</li>
                    <li>Restart Claude Desktop to apply changes</li>
                  </ol>
                </div>
              </div>
            )}

            {activeTab === 'code' && (
              <div className="mcp-tab-content">
                <div className="mcp-config-section">
                  <h4>Configuration for Claude Code</h4>
                  <p className="mcp-tab-description">
                    Claude Code supports direct HTTP/Streamable transport connections.
                  </p>
                  <pre className="mcp-config-code">
                    <code>{codeConfig}</code>
                  </pre>
                  <Button
                    variant="primary"
                    size="small"
                    icon={<Icon name={copied ? "check" : "copy"} size={14} />}
                    onClick={() => handleCopyConfig(codeConfig)}
                  >
                    {copied ? "Copied!" : "Copy Configuration"}
                  </Button>
                </div>

                <div className="mcp-config-location">
                  <h4>Config File Location</h4>
                  <Badge variant="neutral">
                    <code>~/.claude/claude_code_config.json</code>
                  </Badge>
                </div>

                <div className="mcp-instructions">
                  <h4>Setup Instructions</h4>
                  <ol>
                    <li>Copy the configuration above</li>
                    <li>Open your Claude Code config file</li>
                    <li>Add the configuration under the <code>mcpServers</code> key</li>
                    <li>Reload Claude Code to apply changes</li>
                  </ol>
                </div>
              </div>
            )}

            {activeTab === 'web' && (
              <div className="mcp-tab-content">
                <div className="mcp-config-section">
                  <h4>Server URL</h4>
                  <p className="mcp-tab-description">
                    Use this URL for Claude.ai web interface, GPT connectors, or other AI assistants.
                  </p>
                  <div className="mcp-url-display">
                    <code>{serverUrl}</code>
                  </div>
                  <Button
                    variant="primary"
                    size="small"
                    icon={<Icon name={copied ? "check" : "copy"} size={14} />}
                    onClick={handleCopyUrl}
                  >
                    {copied ? "Copied!" : "Copy URL"}
                  </Button>
                </div>

                <div className="mcp-instructions">
                  <h4>Remote Access (Optional)</h4>
                  <p>To share with friends or access remotely, use a tunnel service:</p>
                  <pre className="mcp-config-code">
                    <code>cloudflared tunnel --url {serverUrl}</code>
                  </pre>
                  <p style={{ marginTop: '10px', fontSize: '14px', color: '#666' }}>
                    Or use ngrok: <code>ngrok http {serverUrl.replace('http://localhost:', '')}</code>
                  </p>
                  <p style={{ marginTop: '10px', fontSize: '14px', color: '#666' }}>
                    <strong>Note:</strong> The tunnel will provide a public URL that you can share.
                  </p>
                </div>
              </div>
            )}
          </>
        )}

      </ModalContent>
    </Modal>
  );
};
