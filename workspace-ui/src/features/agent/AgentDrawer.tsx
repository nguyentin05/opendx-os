// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useRef, useState } from 'react';
import { AgentChat } from './AgentChat';
import type { Pending } from './AgentChat';

export const agentDrawerID = 'workspace-agent-drawer';

export interface AgentDrawerProps {
  token: string;
  userID: string;
  open: boolean;
  suspended?: boolean;
  onClose: () => void;
  pendingTurns: Record<string, Pending>;
}

export function AgentDrawer(props: AgentDrawerProps) {
  return <AgentDrawerContents key={JSON.stringify([props.token, props.userID])} {...props} />;
}

function AgentDrawerContents({ token, open, suspended, onClose, pendingTurns }: AgentDrawerProps) {
  const visible = open && !suspended;
  const [opened, setOpened] = useState(visible);
  const wasOpen = useRef(false);
  const closeButton = useRef<HTMLButtonElement>(null);

  if (visible && !opened) setOpened(true);

  useEffect(() => {
    if (visible && !wasOpen.current) closeButton.current?.focus();
    wasOpen.current = open;
  }, [open, visible]);

  useEffect(() => {
    if (!visible) return;
    function handleEscape(event: KeyboardEvent) {
      if (event.key === 'Escape' && !event.defaultPrevented) {
        event.preventDefault();
        onClose();
      }
    }
    window.addEventListener('keydown', handleEscape);
    return () => window.removeEventListener('keydown', handleEscape);
  }, [visible, onClose]);

  return (
    <aside
      id={agentDrawerID}
      className="workspace-agent-drawer"
      role="dialog"
      aria-label="Assistant"
      hidden={!visible}
    >
      <button type="button" ref={closeButton} onClick={onClose}>Close assistant</button>
      {opened && <AgentChat token={token} pendingTurns={pendingTurns} visible={visible} />}
    </aside>
  );
}
