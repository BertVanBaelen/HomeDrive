// What to do with an upload whose name is taken in its folder. "Do this
// for all" applies a choice to the other files added with it (its batch):
// those already waiting and those that find their name taken later. No
// imports, so node --test can load it.

export type Choice = 'replace' | 'keep' | 'skip'

export interface Waiting {
  key: number
  batch: number
  state: string
}

export class Choices {
  #forAll = new Map<number, Choice>()

  // remembered is the choice made for all of a batch, if any.
  remembered(batch: number): Choice | undefined {
    return this.#forAll.get(batch)
  }

  // answer records the choice for the upload asked about and gives the
  // uploads it settles: that one and, for all, the others of its batch
  // that wait for a choice.
  answer<T extends Waiting>(items: T[], key: number, choice: Choice, all: boolean): T[] {
    const asked = items.find((it) => it.key === key && it.state === 'asking')
    if (!asked) return []
    if (all) this.#forAll.set(asked.batch, choice)
    return items.filter((it) => it === asked || (all && it.batch === asked.batch && it.state === 'asking'))
  }
}
