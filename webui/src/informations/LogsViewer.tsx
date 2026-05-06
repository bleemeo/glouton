import { Box, chakra, Heading, HStack, Input, Text, VStack } from "@chakra-ui/react";
import { useEffect, useMemo, useRef, useState } from "react";
import { LuCopy, LuExternalLink } from "react-icons/lu";

import { useTextFetch } from "../api/hooks";

const POLL_MS = 5_000;
const KEEP_LINES = 100;
const NEAR_BOTTOM_PX = 24;

type Severity = "error" | "warn" | "info";

const SEVERITY_COLOR: Record<Severity, string> = {
  error: "var(--chakra-colors-status-crit)",
  warn: "var(--chakra-colors-status-warn)",
  info: "var(--chakra-colors-fg-default)",
};

// Heuristic level inference. Glouton's logger format is
// "YYYY/MM/DD HH:MM:SS.us message", with no level prefix; we infer
// from keywords. Conservative on purpose — better to undercolour
// than to scream "error" on benign messages.
function severityOf(message: string): Severity {
  const m = message.toLowerCase();

  if (/\b(error|errors?:|failed|fatal|panic|crash)\b/.test(m)) return "error";
  if (
    /\b(warn(ing)?|unable|deprecated|skipped|not[- ]?supported|not enabled|cannot|unknown)\b/.test(
      m,
    )
  ) {
    return "warn";
  }

  return "info";
}

type LogLine = {
  raw: string;
  timestamp: string;
  message: string;
  severity: Severity;
};

const LOG_LINE_RE = /^(\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?)\s+(.*)$/;

function parseLines(text: string): LogLine[] {
  const out: LogLine[] = [];
  let pending: LogLine | null = null;

  for (const raw of text.split("\n")) {
    if (raw === "") continue;

    const match = LOG_LINE_RE.exec(raw);
    if (match) {
      if (pending) out.push(pending);
      pending = {
        raw,
        timestamp: match[1],
        message: match[2],
        severity: severityOf(match[2]),
      };
    } else if (pending) {
      // Continuation line (multi-line panic, stack trace, etc.)
      pending.message += "\n" + raw;
      pending.severity = severityOf(pending.message);
    } else {
      // Stray line before any timestamped one: render as-is, no level.
      out.push({ raw, timestamp: "", message: raw, severity: "info" });
    }
  }

  if (pending) out.push(pending);

  return out.slice(-KEEP_LINES);
}

export function LogsViewer() {
  const res = useTextFetch("/data/logs", POLL_MS);
  const [search, setSearch] = useState("");
  const [pinned, setPinned] = useState(false); // user scrolled away from bottom

  const allLines = useMemo(() => parseLines(res.data ?? ""), [res.data]);
  const filtered = useMemo(() => {
    if (!search) return allLines;
    const s = search.toLowerCase();
    return allLines.filter((l) => l.message.toLowerCase().includes(s));
  }, [allLines, search]);

  // Auto-follow tail unless the user has scrolled away from the
  // bottom; in that case freeze position so they can read in peace.
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el || pinned) return;

    el.scrollTop = el.scrollHeight;
  }, [filtered, pinned]);

  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;

    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    setPinned(distanceFromBottom > NEAR_BOTTOM_PX);
  };

  const copy = async () => {
    const text = filtered.map((l) => l.raw).join("\n");
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // Browser denied / no clipboard API; fail silently
    }
  };

  return (
    <VStack align="stretch" gap="2">
      <HStack justify="space-between" gap="3" wrap="wrap">
        <Heading size="sm" color="fg.muted" letterSpacing="0.06em" textTransform="uppercase">
          Glouton logs
        </Heading>
        <HStack gap="2">
          <Input
            size="sm"
            placeholder="Search…"
            value={search}
            onChange={(e) => setSearch(e.currentTarget.value)}
            w={{ base: "180px", md: "240px" }}
            fontFamily="mono"
            fontSize="xs"
          />
          {search ? (
            <Text fontSize="xs" color="fg.subtle" fontFamily="mono" minW="6ch">
              {filtered.length}/{allLines.length}
            </Text>
          ) : null}
          <chakra.button
            type="button"
            onClick={copy}
            title="Copy visible lines"
            px="2"
            py="1.5"
            borderRadius="md"
            borderWidth="1px"
            borderColor="border.subtle"
            bg="surface.subtle"
            color="fg.muted"
            cursor="pointer"
            _hover={{ bg: "surface.canvas", color: "fg.default" }}
          >
            <LuCopy />
          </chakra.button>
          <chakra.a
            href="/data/logs"
            target="_blank"
            rel="noopener noreferrer"
            title="Open full log in a new tab"
            display="inline-flex"
            alignItems="center"
            px="2"
            py="1.5"
            borderRadius="md"
            borderWidth="1px"
            borderColor="border.subtle"
            bg="surface.subtle"
            color="fg.muted"
            _hover={{ bg: "surface.canvas", color: "fg.default", textDecoration: "none" }}
          >
            <LuExternalLink />
          </chakra.a>
        </HStack>
      </HStack>

      <Box
        ref={scrollRef}
        onScroll={onScroll}
        bg="surface.panel"
        borderWidth="1px"
        borderColor="border.subtle"
        borderRadius="lg"
        p="3"
        h="220px"
        overflowY="auto"
        fontFamily="mono"
        fontSize="xs"
        position="relative"
      >
        {res.error ? (
          <Text color="status.crit" fontSize="sm">
            Failed to load logs: {res.error.message}
          </Text>
        ) : res.loading && filtered.length === 0 ? (
          <Text color="fg.muted">Loading…</Text>
        ) : filtered.length === 0 ? (
          <Text color="fg.muted">{search ? "No match" : "No log line yet."}</Text>
        ) : (
          <VStack align="stretch" gap="0.5" minW="0">
            {filtered.map((line, i) => (
              <LogRow
                key={`${line.timestamp}-${i}`}
                line={line}
                search={search}
              />
            ))}
          </VStack>
        )}

        {pinned ? (
          <Box
            position="sticky"
            bottom="0"
            ms="auto"
            mt="-1.5"
            float="right"
            px="2"
            py="0.5"
            borderRadius="full"
            bg="surface.subtle"
            borderWidth="1px"
            borderColor="border.default"
            color="fg.muted"
            fontSize="2xs"
            fontFamily="mono"
            cursor="pointer"
            onClick={() => {
              const el = scrollRef.current;
              if (el) {
                el.scrollTop = el.scrollHeight;
                setPinned(false);
              }
            }}
            _hover={{ color: "fg.default" }}
            title="Click to resume tailing"
          >
            paused — jump to bottom
          </Box>
        ) : null}
      </Box>
    </VStack>
  );
}

function LogRow({ line, search }: { line: LogLine; search: string }) {
  const color = SEVERITY_COLOR[line.severity];

  return (
    <HStack gap="2" align="start" minW="0">
      <Text color="fg.subtle" whiteSpace="nowrap" flexShrink={0}>
        {line.timestamp.slice(11)}
      </Text>
      <Box color={color} whiteSpace="pre-wrap" wordBreak="break-word" minW="0" flex="1">
        {highlight(line.message, search)}
      </Box>
    </HStack>
  );
}

function highlight(text: string, search: string) {
  if (!search) return text;

  const lower = text.toLowerCase();
  const needle = search.toLowerCase();
  const parts: React.ReactNode[] = [];
  let cursor = 0;

  while (cursor < text.length) {
    const found = lower.indexOf(needle, cursor);

    if (found < 0) {
      parts.push(text.slice(cursor));
      break;
    }

    if (found > cursor) parts.push(text.slice(cursor, found));

    parts.push(
      <chakra.mark
        key={`m-${found}`}
        bg="kpi.warn.bg"
        color="kpi.warn.fg"
        borderRadius="2px"
        px="0.5"
      >
        {text.slice(found, found + needle.length)}
      </chakra.mark>,
    );

    cursor = found + needle.length;
  }

  return parts;
}
