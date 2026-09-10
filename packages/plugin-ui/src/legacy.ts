import type { CardContext, CardInstance, DocketPluginUI, TaskCardModule } from './contracts';
/** Deliberately preserves the old arguments and the old task reference. */
export function adaptLegacyPluginUI(plugin: DocketPluginUI): DocketPluginUI {
  return { cards: plugin.cards?.map((card): TaskCardModule => ({
    type: card.type, appliesTo: task => card.appliesTo(task),
    mount(element, context): CardInstance {
      const legacy: CardContext = { workspace: context.workspace, task: context.task, pluginBase: context.pluginBase, refresh: context.refresh };
      const instance = card.mount(element, legacy);
      return { update: task => instance.update(task), destroy: () => instance.destroy() };
    },
  })), referenceResolvers: plugin.referenceResolvers };
}
