import { parse, stringify } from 'yaml';
import type { TemplateParameter } from './types';

export function cleanApplicationManifest(manifest: string, namespace?: string, name?: string): string {
  const quoted = manifest.replace(/(:\s*|^\s*-\s*)(\{\{[A-Za-z][A-Za-z0-9_]*\}\})(\s*(?:#.*)?$)/gm, '$1"$2"$3');
  const object = parse(quoted) as Record<string, unknown>;
  if (!object || object.kind !== 'SparkApplication' || !object.apiVersion) throw new Error('A SparkApplication manifest with an apiVersion is required');
  const metadata = { ...(object.metadata as Record<string, unknown> || {}) };
  const annotations = { ...(metadata.annotations as Record<string, unknown> || {}) };
  delete annotations['kubectl.kubernetes.io/last-applied-configuration'];
  delete annotations['spark-control-center.io/submitted-by'];
  if (namespace) metadata.namespace = namespace;
  if (name) metadata.name = name;
  if (!metadata.name) throw new Error('metadata.name is required');
  const cleanedMetadata = Object.fromEntries(['name', 'namespace', 'labels'].filter((key) => metadata[key] != null).map((key) => [key, metadata[key]]));
  if (Object.keys(annotations).length) cleanedMetadata.annotations = annotations;
  return stringify({ apiVersion: object.apiVersion, kind: object.kind, metadata: cleanedMetadata, spec: object.spec }, { lineWidth: 0 });
}

export function renderTemplateManifest(manifest: string, namespace: string, parameters: TemplateParameter[], values: Record<string, string>): string {
  const replacements = Object.fromEntries(parameters.map((p) => {
    const value = values[p.name] || p.default || '';
    if (p.required && !value) throw new Error(`Parameter ${p.name} is required`);
    if (p.enum?.length && value && !p.enum.includes(value)) throw new Error(`Parameter ${p.name} is not an allowed value`);
    if (p.pattern && value && !new RegExp(p.pattern).test(value)) throw new Error(`Parameter ${p.name} does not match its pattern`);
    let typed: unknown = value;
    if (value && ['integer', 'number'].includes(p.type)) {
      typed = Number(value);
      if (!Number.isFinite(typed) || p.type === 'integer' && !Number.isInteger(typed)) throw new Error(`Parameter ${p.name} must be ${p.type}`);
    } else if (value && p.type === 'boolean') {
      if (!['true', 'false'].includes(value)) throw new Error(`Parameter ${p.name} must be boolean`);
      typed = value === 'true';
    }
    return [`{{${p.name}}}`, typed];
  }));
  const render = (value: unknown): unknown => {
    if (typeof value === 'string') {
      if (Object.hasOwn(replacements, value)) return replacements[value];
      return Object.entries(replacements).reduce((text, [key, replacement]) => text.replaceAll(key, String(replacement)), value);
    }
    if (Array.isArray(value)) return value.map(render);
    if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key, child]) => [key, render(child)]));
    return value;
  };
  const result = stringify(render(parse(cleanApplicationManifest(manifest, namespace))), { lineWidth: 0 });
  if (/\{\{[A-Za-z][A-Za-z0-9_]*\}\}/.test(result)) throw new Error('Unresolved template parameter');
  return result;
}
