export type Location = {platform:string; wsl?:string};

export const locationKey = (source:Location) => source.wsl ? `wsl:${source.wsl}` : source.platform;

export function locationLabel({platform,wsl}:Location) {
 if (platform==='wsl') return wsl ? `WSL · ${wsl}` : 'WSL';
 return ({windows:'Windows',linux:'Linux',darwin:'macOS'} as Record<string,string>)[platform] ?? (platform || '本机');
}

const wslPrefix = /^(?:\\\\[?.]\\UNC[\\/])?(?:[\\/]{2})?wsl(?:\.localhost|\$)[\\/]([^\\/:]+)(?=[\\/]|$)/i;

export function displayPath(path:string) {
 const prefix = wslPrefix.exec(path);
 return prefix ? path.slice(prefix[0].length).replaceAll('\\','/') || '/' : path;
}

export function homeRelativePath(path:string, home:string) {
 const absolute=displayPath(path);
 const prefix=displayPath(home).replace(/\/+$/,'');
 if (prefix && absolute===prefix) return '~';
 return prefix && absolute.startsWith(`${prefix}/`) ? `~${absolute.slice(prefix.length)}` : absolute;
}

export function displayScanWarning(warning:string) {
 // Scan errors may include two UNC paths when a system call names its target.
 return warning.replace(/(?:\\\\[?.]\\UNC\\|[\\/]{2})wsl(?:\.localhost|\$)[\\/][^\\/:]+(?:[\\/][^:\r\n]*?)?(?=: |$)/gi,displayPath);
}
