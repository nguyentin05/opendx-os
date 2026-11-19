// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { AgentChat } from './AgentChat';
import type { Pending } from './AgentChat';

export const agentDrawerID = 'workspace-agent-drawer';
const overlayQuery = '(width < 64rem)';

function closeDialog(dialog: HTMLDialogElement) {
  if (!dialog.open) return;
  if (typeof dialog.close === 'function') dialog.close();
  else dialog.removeAttribute('open');
}

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
  const [modal, setModal] = useState(false);
  const drawer = useRef<HTMLDialogElement>(null);
  const wasOpen = useRef(false);
  const closeButton = useRef<HTMLButtonElement>(null);

  if (visible && !opened) setOpened(true);

  useLayoutEffect(() => {
    const dialog = drawer.current;
    if (!visible || !dialog) return;

    const viewport = window.matchMedia?.(overlayQuery);
    function syncPresentation() {
      if (!dialog) return;
      const isOverlay = viewport?.matches ?? false;
      const focused = document.activeElement;
      const focusInside = focused instanceof HTMLElement && dialog.contains(focused);
      closeDialog(dialog);
      if (isOverlay && typeof dialog.showModal === 'function') dialog.showModal();
      else dialog.setAttribute('open', '');
      if (focusInside) focused.focus();
      setModal(isOverlay);
    }

    syncPresentation();
    viewport?.addEventListener('change', syncPresentation);
    return () => {
      viewport?.removeEventListener('change', syncPresentation);
      closeDialog(dialog);
    };
  }, [visible]);

  useLayoutEffect(() => {
    if (visible && !wasOpen.current) closeButton.current?.focus();
    wasOpen.current = open;
  }, [open, visible]);

  function close() {
    if (drawer.current) closeDialog(drawer.current);
    onClose();
  }

  useEffect(() => {
    if (!visible) return;
    function handleEscape(event: KeyboardEvent) {
      if (event.key === 'Escape' && !event.defaultPrevented) {
        event.preventDefault();
        close();
      }
    }
    window.addEventListener('keydown', handleEscape);
    return () => window.removeEventListener('keydown', handleEscape);
  }, [visible, onClose]);

  return (
    <dialog
      ref={drawer}
      id={agentDrawerID}
      className="workspace-agent-drawer"
      aria-label="Assistant"
      aria-modal={visible && modal ? true : undefined}
      onCancel={event => {
        event.preventDefault();
        close();
      }}
    >
      <button type="button" ref={closeButton} onClick={close}>Close assistant</button>
      {opened && <AgentChat token={token} pendingTurns={pendingTurns} visible={visible} />}
    </dialog>
  );
}
