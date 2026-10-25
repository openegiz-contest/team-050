export type MapPoint = {x: number; y: number};
export type MapNode = MapPoint & {id: string};
export type VehicleTrack = {id: string; edgeId: string; nodeId: string; progress: number};
export type VehiclePlacement = {point: MapPoint; heading: number};
export type MapProjection = (x: number, y: number) => MapPoint;

/** Physical anchors never depend on zoom, fleet size or queue order. */
export function placeVehicles(tracks: VehicleTrack[], nodes: MapNode[], project: MapProjection): Map<string, VehiclePlacement> {
  const byId = new Map(nodes.map(item => [item.id, item]));
  const out = new Map<string, VehiclePlacement>();
  for (const track of tracks) {
    const [from, to] = track.edgeId.split('>');
    const a = byId.get(from), b = byId.get(to);
    if (a && b) {
      const start = project(a.x, a.y), end = project(b.x, b.y);
      const progress = Number.isFinite(track.progress) ? Math.max(0, Math.min(1, track.progress)) : 0;
      out.set(track.id, {point: {x: start.x + (end.x-start.x)*progress, y: start.y + (end.y-start.y)*progress}, heading: Math.atan2(end.y-start.y, end.x-start.x)*180/Math.PI});
    } else {
      const at = byId.get(track.nodeId);
      if (at) out.set(track.id, {point: project(at.x, at.y), heading: 0});
    }
  }
  return out;
}

/** Group labels only; never write a group offset back into physical anchors. */
export function groupVehicles(placements: Map<string, VehiclePlacement>, radius: number): string[][] {
  const groups: string[][] = [];
  for (const [id, placement] of placements) {
    const group = groups.find(items => {
      const first = placements.get(items[0])!;
      return Math.hypot(first.point.x-placement.point.x, first.point.y-placement.point.y) < radius;
    });
    if (group) group.push(id); else groups.push([id]);
  }
  return groups;
}
