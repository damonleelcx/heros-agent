"use client";

import { useEffect } from "react";
import { recordRecent, type PaletteSubject } from "./commandPalette";

/**
 * RecordVisit remembers that the operator looked at a subject, so the command palette can offer it
 * first next time (FR32).
 *
 * It renders nothing. It exists because "the console anticipates the next move" is mostly this: the
 * three tenants you were just looking at should be the three the palette shows before you type
 * anything, because during an incident you are going back and forth between the same few subjects.
 *
 * What it stores is a label, a hint and a URL — no session material, no personal data, nothing the
 * palette could not already show this operator. It is a convenience that can fail silently and cost
 * nothing (see `recordRecent`).
 */
export function RecordVisit({ subject }: { subject: PaletteSubject }) {
  useEffect(() => {
    recordRecent(subject);
    // The subject is derived from the page's own params, so its identity is stable for this route.
  }, [subject.href, subject.hint]); // eslint-disable-line react-hooks/exhaustive-deps
  return null;
}
