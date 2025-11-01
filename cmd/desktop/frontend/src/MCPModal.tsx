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
import { Modal, ModalContent, ModalActions, Button, Icon, Badge } from './design-system';
import { GetExecutablePath } from '../wailsjs/go/main/DesktopApp';
import './MCPModal.css';

interface MCPModalProps {
  isOpen: boolean;
  onClose: () => void;
}

const detectPlatform = (): 'macOS' | 'Windows' | 'Linux' => {
  const platform = window.navigator.platform.toLowerCase();

  if (platform.includes('mac')) return 'macOS';
  if (platform.includes('win')) return 'Windows';
  return 'Linux';
};

const getConfigForPlatform = (platform: string) => {
  const configs = {
    'macOS': {
      command: '/Applications/BlueSnake.app/Contents/MacOS/BlueSnake',
      configPath: '~/Library/Application Support/Claude/claude_desktop_config.json'
    },
    'Windows': {
      command: 'C:\\Program Files\\BlueSnake\\BlueSnake.exe',
      configPath: '%APPDATA%\\Claude\\claude_desktop_config.json'
    },
    'Linux': {
      command: '/opt/bluesnake/bluesnake',
      configPath: '~/.config/Claude/claude_desktop_config.json'
    }
  };

  return configs[platform as keyof typeof configs];
};

export const MCPModal: React.FC<MCPModalProps> = ({ isOpen, onClose }) => {
  const [copied, setCopied] = useState(false);
  const [executablePath, setExecutablePath] = useState<string>('');
  const [isLoading, setIsLoading] = useState(true);
  const platform = detectPlatform();
  const platformConfig = getConfigForPlatform(platform);

  // Fetch the actual executable path when modal opens
  useEffect(() => {
    if (isOpen) {
      setIsLoading(true);
      GetExecutablePath()
        .then((path: string) => {
          setExecutablePath(path);
          setIsLoading(false);
        })
        .catch((error: any) => {
          console.error('Failed to get executable path:', error);
          // Fallback to default path
          setExecutablePath(platformConfig.command);
          setIsLoading(false);
        });
    }
  }, [isOpen, platformConfig.command]);

  const configJSON = JSON.stringify(
    {
      mcpServers: {
        bluesnake: {
          command: executablePath || platformConfig.command,
          args: ['mcp']
        }
      }
    },
    null,
    2
  );

  const handleCopyConfig = async () => {
    try {
      await navigator.clipboard.writeText(configJSON);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (error) {
      console.error('Failed to copy configuration:', error);
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
          <p>Access BlueSnake's web crawling capabilities directly from Claude AI conversations.</p>
        </div>

        {isLoading ? (
          <div className="mcp-loading">
            <p>Detecting executable path...</p>
          </div>
        ) : (
          <>
            <div className="mcp-config-section">
              <h4>Configuration for {platform}</h4>
              <pre className="mcp-config-code">
                <code>{configJSON}</code>
              </pre>
              <Button
                variant="primary"
                size="small"
                icon={<Icon name={copied ? "check" : "copy"} size={14} />}
                onClick={handleCopyConfig}
              >
                {copied ? "Copied!" : "Copy Configuration"}
              </Button>
            </div>

            <div className="mcp-config-location">
              <h4>Config File Location</h4>
              <Badge variant="neutral">
                <code>{platformConfig.configPath}</code>
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
          </>
        )}
      </ModalContent>
    </Modal>
  );
};
